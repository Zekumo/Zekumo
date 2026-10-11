package zekumo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func realtimePair(t *testing.T) (*Realtime, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			accepted <- conn
		}
	}))
	t.Cleanup(server.Close)
	c := New(Options{AppID: "test", BaseURL: server.URL})
	c.SetToken("synthetic")
	rt := c.Realtime()
	if err := rt.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	peer := <-accepted
	t.Cleanup(func() { _ = rt.Close(); _ = peer.Close() })
	return rt, peer
}

func TestRealtimeConcurrentSendSerializesWrites(t *testing.T) {
	rt, peer := realtimePair(t)
	const count = 100
	read := make(chan error, 1)
	go func() {
		_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
		ids := map[int]bool{}
		for i := 0; i < count; i++ {
			var env struct {
				Type string `json:"type"`
				Data struct {
					ID int `json:"id"`
				} `json:"data"`
			}
			if err := peer.ReadJSON(&env); err != nil {
				read <- err
				return
			}
			if env.Type != "example" || ids[env.Data.ID] {
				read <- fmt.Errorf("duplicate or malformed frame: %+v", env)
				return
			}
			ids[env.Data.ID] = true
		}
		read <- nil
	}()
	var wg sync.WaitGroup
	failures := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if err := rt.Send("example", map[string]int{"id": id}); err != nil {
				failures <- err
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if err := <-read; err != nil {
		t.Fatal(err)
	}
}

func TestRealtimePongsAndGetRoomObject(t *testing.T) {
	rt, peer := realtimePair(t)
	pong := make(chan string, 1)
	peer.SetPongHandler(func(value string) error { pong <- value; return nil })
	read := make(chan error, 1)
	go func() {
		_ = peer.SetReadDeadline(time.Now().Add(3 * time.Second))
		var env envelope
		if err := peer.ReadJSON(&env); err != nil {
			read <- err
			return
		}
		if env.Type != "room.get" || string(env.Data) != "{}" {
			read <- fmt.Errorf("room.get must carry an empty object: %+v", env)
			return
		}
		// Keep reading so the control-frame handler is called even if the data frame arrived first.
		_, _, err := peer.ReadMessage()
		read <- err
	}()
	if err := peer.WriteControl(websocket.PingMessage, []byte("probe"), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := rt.GetRoom(); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-pong:
		if value != "probe" {
			t.Fatalf("pong payload=%q", value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("missing pong")
	}
	_ = rt.Close()
	<-read
}

func TestRealtimeRejectsInvalidPayload(t *testing.T) {
	rt, peer := realtimePair(t)
	if err := rt.Send("example", make(chan int)); err == nil {
		t.Fatal("invalid JSON payload silently accepted")
	}
	if err := rt.Send("valid", json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	var env envelope
	if err := peer.ReadJSON(&env); err != nil {
		t.Fatal(err)
	}
	if env.Type != "valid" {
		t.Fatalf("invalid payload reached the wire: %s", env.Type)
	}
}
