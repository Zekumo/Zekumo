package main

import (
	"fmt"
	"os"
	"time"
)

func phaseRealtime(s *state) {
	// WebSocket: rooms + realtime state + chat.
	wsAlice, err := dialWS(s.alice.Token, "alice")
	step("ws connect alice", err)
	wsBob, err := dialWS(s.bob.Token, "bob")
	step("ws connect bob", err)
	if failed > 0 {
		fmt.Printf("\n%d step(s) failed before WS flow; aborting\n", failed)
		os.Exit(1)
	}

	var roomID string
	_ = wsAlice.send("room.create", map[string]any{"name": "测试房", "max_players": 4})
	created, err := wsAlice.expect("room.created")
	if err == nil {
		roomID, _ = created["id"].(string)
	}
	step("room.create", err)

	_ = wsBob.send("room.join", map[string]any{"room_id": roomID})
	_, err = wsBob.expect("room.joined")
	step("room.join (bob)", err)
	_, err = wsAlice.expect("room.member_joined")
	step("member_joined broadcast (alice)", err)

	_ = wsAlice.send("room.state", map[string]any{"x": 10, "y": 20, "hp": 100})
	state, err := wsBob.expect("room.state")
	if err == nil {
		if inner, ok := state["state"].(map[string]any); !ok || inner["x"] != float64(10) {
			err = fmt.Errorf("unexpected state payload: %v", state)
		}
	}
	step("room.state relayed to bob", err)

	_ = wsBob.send("room.msg", map[string]any{"action": "attack", "target": "slime"})
	m, err := wsAlice.expect("room.msg")
	if err == nil {
		if inner, ok := m["data"].(map[string]any); !ok || inner["action"] != "attack" {
			err = fmt.Errorf("unexpected msg payload: %v", m)
		}
	}
	step("room.msg relayed to alice", err)

	// Chat: both subscribe to world channel; alice speaks; bob hears it.
	_ = wsAlice.send("chat.sub", map[string]string{"channel": "world"})
	_, _ = wsAlice.expect("chat.subbed")
	_ = wsBob.send("chat.sub", map[string]string{"channel": "world"})
	_, _ = wsBob.expect("chat.subbed")
	_ = wsAlice.send("chat.send", map[string]string{"channel": "world", "content": "大家好呀!"})
	chatMsg, err := wsBob.expect("chat.msg")
	if err == nil && chatMsg["content"] != "大家好呀!" {
		err = fmt.Errorf("unexpected chat payload: %v", chatMsg)
	}
	step("chat delivered via ws", err)

	time.Sleep(300 * time.Millisecond) // async persistence
	var history struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	step("chat history via http", call("GET", "/v1/chat/history?channel=world", s.bob.Token, nil, &history))
	if len(history.Messages) == 0 || history.Messages[0].Content != "大家好呀!" {
		step("history contains message", fmt.Errorf("unexpected history: %+v", history.Messages))
	} else {
		step("history contains message", nil)
	}

	_ = wsBob.send("room.leave", nil)
	_, err = wsBob.expect("room.left")
	step("room.leave (bob)", err)
	_, err = wsAlice.expect("room.member_left")
	step("member_left broadcast (alice)", err)

	wsAlice.conn.Close()
	wsBob.conn.Close()
}
