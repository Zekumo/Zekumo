package zekumo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func lifecycleServer(t *testing.T, beforeUpgrade func()) (*Realtime, <-chan *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if beforeUpgrade != nil {
			beforeUpgrade()
		}
		peer, err := (&websocket.Upgrader{}).Upgrade(w, req, nil)
		if err == nil {
			accepted <- peer
		}
	}))
	t.Cleanup(server.Close)
	rt := New(Options{AppID: "test", BaseURL: server.URL}).Realtime()
	t.Cleanup(func() {
		_ = rt.Close()
		for {
			select {
			case p := <-accepted:
				_ = p.Close()
			default:
				return
			}
		}
	})
	return rt, accepted
}

func receivePeer(t *testing.T, peers <-chan *websocket.Conn) *websocket.Conn {
	t.Helper()
	select {
	case p := <-peers:
		t.Cleanup(func() { _ = p.Close() })
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("no websocket accepted")
		return nil
	}
}

func assertPeerClosed(t *testing.T, peer *websocket.Conn) {
	t.Helper()
	_ = peer.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, err := peer.ReadMessage()
	if err == nil {
		t.Fatal("expected connection closure")
	}
	if e, ok := err.(interface{ Timeout() bool }); ok && e.Timeout() {
		t.Fatal("socket remained open until read timeout")
	}
}

func TestRealtimeCancellationClosesSilentPeer(t *testing.T) {
	rt, peers := lifecycleServer(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := rt.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	peer := receivePeer(t, peers)
	cancel()
	assertPeerClosed(t, peer)
	if err := rt.Send("ping", nil); err == nil {
		t.Fatal("send succeeded after cancellation")
	}
}

func TestRealtimeCloseDuringHandshake(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	rt, peers := lifecycleServer(t, func() { close(entered); <-release })
	result := make(chan error, 1)
	go func() { result <- rt.Connect(context.Background()) }()
	<-entered
	_ = rt.Close()
	close(release)
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("Connect succeeded after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("Connect did not stop")
	}
	select {
	case p := <-peers:
		defer p.Close()
		assertPeerClosed(t, p)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRealtimeRejectsOverlappingConnect(t *testing.T) {
	rt, peers := lifecycleServer(t, nil)
	if err := rt.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = receivePeer(t, peers)
	if err := rt.Connect(context.Background()); err == nil {
		t.Fatal("overlapping Connect succeeded")
	}
}

func TestRealtimeConnectCanceledContext(t *testing.T) {
	rt, _ := lifecycleServer(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := rt.Connect(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Connect error = %v", err)
	}
}

func TestRealtimeCancelDuringSilentHandshake(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	rt, _ := lifecycleServer(t, func() { close(entered); <-release })
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- rt.Connect(ctx) }()
	<-entered
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Connect error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt HTTP upgrade")
	}
}

func TestRealtimeOldReaderCannotClearNewSession(t *testing.T) {
	rt, peers := lifecycleServer(t, nil)
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	rt.On("hold", func(_ json.RawMessage) { close(entered); <-release; close(returned) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := rt.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	oldPeer := receivePeer(t, peers)
	if err := oldPeer.WriteJSON(envelope{Type: "hold"}); err != nil {
		t.Fatal(err)
	}
	<-entered
	rt.mu.Lock()
	oldSession := rt.session
	rt.mu.Unlock()
	cancel()
	if err := rt.Connect(context.Background()); err != nil {
		close(release)
		t.Fatal(err)
	}
	newPeer := receivePeer(t, peers)
	close(release)
	<-returned
	// Exercise the former cancellation callback deterministically after redial.
	// The blocked reader also exits concurrently after its handler returns.
	rt.mu.Lock()
	current := rt.session
	rt.mu.Unlock()
	if rt.finish(oldSession, ErrReplaced) {
		t.Fatal("stale session changed ownership")
	}
	if err := rt.Send("probe", nil); err != nil {
		t.Fatal(err)
	}
	_ = newPeer.SetReadDeadline(time.Now().Add(time.Second))
	var env envelope
	if err := newPeer.ReadJSON(&env); err != nil {
		t.Fatal(err)
	}
	rt.mu.Lock()
	live := rt.session == current && rt.conn != nil && !rt.closed
	rt.mu.Unlock()
	if !live || rt.Err() != nil {
		t.Fatal("old reader changed the new session")
	}
}

func TestRealtimeReplacedErrorVisibleInHandler(t *testing.T) {
	rt, peers := lifecycleServer(t, nil)
	result := make(chan error, 1)
	rt.On("replaced", func(_ json.RawMessage) { result <- rt.Err() })
	if err := rt.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	peer := receivePeer(t, peers)
	if err := peer.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "replaced"), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrReplaced) {
			t.Fatalf("handler Err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("missing replaced event")
	}
	if err := rt.Connect(context.Background()); err == nil {
		t.Fatal("terminal client reconnected")
	}
}

func TestRealtimeConcurrentConnectHasOneOwner(t *testing.T) {
	rt, peers := lifecycleServer(t, nil)
	const count = 16
	start := make(chan struct{})
	result := make(chan error, count)
	for range count {
		go func() { <-start; result <- rt.Connect(context.Background()) }()
	}
	close(start)
	successes := 0
	for range count {
		select {
		case err := <-result:
			if err == nil {
				successes++
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Connect hung")
		}
	}
	if successes != 1 {
		t.Fatalf("successful Connect calls = %d", successes)
	}
	_ = receivePeer(t, peers)
}

func TestRealtimeCloseDuringBackoff(t *testing.T) {
	rt, peers := lifecycleServer(t, nil)
	stopped := make(chan struct{})
	rt.On("reconnecting", func(_ json.RawMessage) { _ = rt.Close(); close(stopped) })
	if err := rt.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	peer := receivePeer(t, peers)
	_ = peer.Close()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("no reconnect notification")
	}
	select {
	case p := <-peers:
		_ = p.Close()
		t.Fatal("reconnected after Close")
	case <-time.After(rtBackoffMin + 100*time.Millisecond):
	}
}

func TestRealtimeReconnectPublishesNewSocket(t *testing.T) {
	rt, peers := lifecycleServer(t, nil)
	welcomed := make(chan struct{}, 2)
	rt.On("welcome", func(_ json.RawMessage) { welcomed <- struct{}{} })
	if err := rt.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	oldPeer := receivePeer(t, peers)
	_ = oldPeer.Close()
	peer := receivePeer(t, peers)
	if err := peer.WriteJSON(envelope{Type: "welcome"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-welcomed:
	case <-time.After(time.Second):
		t.Fatal("new reader did not start")
	}
	if err := rt.Send("probe", nil); err != nil {
		t.Fatal(err)
	}
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	var env envelope
	if err := peer.ReadJSON(&env); err != nil {
		t.Fatal(err)
	}
	if env.Type != "probe" {
		t.Fatalf("frame = %s", env.Type)
	}
}

func TestRealtimeSendCancelAndRedial(t *testing.T) {
	rt, peers := lifecycleServer(t, nil)
	for range 10 {
		ctx, cancel := context.WithCancel(context.Background())
		if err := rt.Connect(ctx); err != nil {
			cancel()
			t.Fatal(err)
		}
		peer := receivePeer(t, peers)
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			for {
				if _, _, err := peer.ReadMessage(); err != nil {
					return
				}
			}
		}()
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 10 {
					_ = rt.Ping()
				}
			}()
		}
		cancel()
		wg.Wait()
		select {
		case <-readDone:
		case <-time.After(time.Second):
			t.Fatal("peer reader did not stop")
		}
	}
}

func TestRealtimePreservesCustomDialers(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint("legacy=", legacy), func(t *testing.T) {
			rt, peers := lifecycleServer(t, nil)
			called := false
			dialer := *websocket.DefaultDialer
			if legacy {
				dialer.NetDial = func(network, addr string) (net.Conn, error) { called = true; return net.Dial(network, addr) }
			} else {
				dialer.NetDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
					called = true
					return (&net.Dialer{}).DialContext(ctx, network, addr)
				}
			}
			rt.Dialer = &dialer
			if err := rt.Connect(context.Background()); err != nil {
				t.Fatal(err)
			}
			_ = receivePeer(t, peers)
			if !called {
				t.Fatal("custom dial hook was bypassed")
			}
			if legacy && dialer.NetDialContext != nil {
				t.Fatal("caller-owned dialer mutated")
			}
		})
	}
}
