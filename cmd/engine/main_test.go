package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeController accepts one engine, walks it through hello → assigned →
// step → reseed, and reports what it saw.
func TestEngineSession(t *testing.T) {
	upgrader := websocket.Upgrader{}
	got := make(chan stateMsg, 8)
	helloSeen := make(chan helloMsg, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/engine" {
			t.Errorf("engine dialled %s, want /engine", r.URL.Path)
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()

		var hello helloMsg
		if err := ws.ReadJSON(&hello); err != nil {
			t.Errorf("read hello: %v", err)
			return
		}
		helloSeen <- hello

		ws.WriteJSON(map[string]interface{}{"type": "assigned", "position": 44, "row": 4, "col": 4, "generation": 100})
		readState := func() {
			var st stateMsg
			if err := ws.ReadJSON(&st); err != nil {
				t.Errorf("read state: %v", err)
				return
			}
			got <- st
		}
		readState() // initial state after assignment

		var halo [9][9]bool
		ws.WriteJSON(map[string]interface{}{"type": "step", "generation": 101, "halo": halo})
		readState()

		ws.WriteJSON(map[string]interface{}{"type": "reseed"})
		readState()
	}))
	defer server.Close()

	t.Setenv("CONTROLLER_URL", strings.Replace(server.URL, "http://", "ws://", 1))
	t.Setenv("ENGINE_ID", "test-engine")
	t.Setenv("DISPLAY_NAME", "Tester")
	engine := NewEngine()
	if !strings.HasSuffix(engine.controlURL, "/engine") {
		t.Fatalf("controlURL should end in /engine, got %s", engine.controlURL)
	}

	done := make(chan error, 1)
	go func() { done <- engine.session() }()

	select {
	case hello := <-helloSeen:
		if hello.Type != "hello" || hello.EngineID != "test-engine" || hello.DisplayName != "Tester" || hello.Version != protocolVersion {
			t.Fatalf("unexpected hello: %+v", hello)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no hello within 2s")
	}

	expect := func(wantGen int64, label string) stateMsg {
		select {
		case st := <-got:
			if st.Type != "state" || st.Generation != wantGen || len(st.Grid) != 7 || len(st.Grid[0]) != 7 {
				t.Fatalf("%s: unexpected state %+v", label, st)
			}
			return st
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: no state within 2s", label)
		}
		return stateMsg{}
	}
	expect(100, "after assigned")
	expect(101, "after step")
	reseeded := expect(101, "after reseed")

	alive := 0
	for _, row := range reseeded.Grid {
		for _, cell := range row {
			if cell {
				alive++
			}
		}
	}
	if alive == 0 {
		t.Fatal("reseed produced an empty section")
	}
	if engine.position != 44 {
		t.Fatalf("position = %d, want 44", engine.position)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("session did not end after server closed")
	}
}

func TestStateMsgWireFormat(t *testing.T) {
	data, _ := json.Marshal(stateMsg{Type: "state", Generation: 7, Grid: [][]bool{{true}}})
	if string(data) != `{"type":"state","generation":7,"grid":[[true]]}` {
		t.Fatalf("wire format drifted: %s", data)
	}
}
