package main

// Engine transport: every engine opens ONE WebSocket to the controller at
// /engine and keeps it open. The controller never dials an engine, which is
// what lets engines run behind NAT on someone's laptop.
//
//   engine -> controller   {"type":"hello","engineId":"…","displayName":"…","version":1}
//   controller -> engine   {"type":"assigned","position":44,"row":4,"col":4,"generation":123}
//   controller -> engine   {"type":"step","generation":124,"halo":[9][9]bool}
//   engine -> controller   {"type":"state","generation":124,"grid":[7][7]bool}
//   controller -> engine   {"type":"reseed"}
//   controller -> engine   {"type":"error","reason":"…"}   (then close)
//
// All controller-side state is still owned by the message-processor goroutine;
// this file only moves bytes between that goroutine and the socket.

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/gorilla/websocket"
)

const (
	engineProtocolVersion = 1
	engineHelloTimeout    = 10 * time.Second
	engineReadTimeout     = 90 * time.Second // must exceed the ping interval comfortably
	enginePingInterval    = 30 * time.Second
	engineWriteTimeout    = 5 * time.Second
	engineMaxMessageBytes = 8 * 1024
	engineSendBuffer      = 8
	maxDisplayNameRunes   = 32
	maxStateMsgsPerSecond = 20 // a well-behaved engine sends ~4/s
)

// clientIdentity works out where an engine is connecting from. Anything that
// arrived through Cloudflare carries CF-Connecting-IP and is "public";
// anything else from a private address (the compose network, the LAN) is a
// house engine.
func clientIdentity(r *http.Request) (ip string, public bool) {
	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
		return cf, true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	parsed := net.ParseIP(host)
	if parsed != nil && (parsed.IsLoopback() || parsed.IsPrivate() || parsed.IsLinkLocalUnicast()) {
		return host, false
	}
	return host, true
}

var engineIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

type engineHello struct {
	Type        string `json:"type"`
	EngineID    string `json:"engineId"`
	DisplayName string `json:"displayName"`
	Version     int    `json:"version"`
}

type engineStateMsg struct {
	Type       string   `json:"type"`
	Generation int      `json:"generation"`
	Grid       [][]bool `json:"grid"`
}

type ctrlAssigned struct {
	Type       string `json:"type"`
	Position   int    `json:"position"`
	Row        int    `json:"row"`
	Col        int    `json:"col"`
	Generation int64  `json:"generation"`
}

type ctrlStep struct {
	Type       string     `json:"type"`
	Generation int64      `json:"generation"`
	Halo       [9][9]bool `json:"halo"`
}

type ctrlSimple struct {
	Type   string `json:"type"`
	Reason string `json:"reason,omitempty"`
}

// engineConn is one live engine socket. send is drained by a single writer
// goroutine so the processor never blocks on a slow network.
type engineConn struct {
	ws       *websocket.Conn
	send     chan []byte
	done     chan struct{}
	closeOne sync.Once
	engineID string
	remote   string
}

func newEngineConn(ws *websocket.Conn, engineID string) *engineConn {
	return &engineConn{
		ws:       ws,
		send:     make(chan []byte, engineSendBuffer),
		done:     make(chan struct{}),
		engineID: engineID,
		remote:   ws.RemoteAddr().String(),
	}
}

// enqueue hands a message to the writer without blocking. A full buffer
// means the engine is not keeping up; dropping a step is the right call —
// the barrier's miss budget handles the rest.
func (ec *engineConn) enqueue(v interface{}) bool {
	data, err := json.Marshal(v)
	if err != nil {
		return false
	}
	select {
	case ec.send <- data:
		return true
	case <-ec.done:
		return false
	default:
		log.Printf("Engine %s: send buffer full, dropping %T", ec.engineID, v)
		return false
	}
}

func (ec *engineConn) close() {
	ec.closeOne.Do(func() {
		close(ec.done)
		ec.ws.Close()
	})
}

func (ec *engineConn) writeLoop() {
	ticker := time.NewTicker(enginePingInterval)
	defer ticker.Stop()
	for {
		select {
		case data := <-ec.send:
			ec.ws.SetWriteDeadline(time.Now().Add(engineWriteTimeout))
			if err := ec.ws.WriteMessage(websocket.TextMessage, data); err != nil {
				ec.close()
				return
			}
		case <-ticker.C:
			ec.ws.SetWriteDeadline(time.Now().Add(engineWriteTimeout))
			if err := ec.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				ec.close()
				return
			}
		case <-ec.done:
			return
		}
	}
}

// sanitiseDisplayName keeps names safe to render on the public page.
func sanitiseDisplayName(raw string) string {
	var b strings.Builder
	count := 0
	for _, r := range strings.TrimSpace(raw) {
		if !unicode.IsPrint(r) || unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
		count++
		if count >= maxDisplayNameRunes {
			break
		}
	}
	return b.String()
}

func validGrid(grid [][]bool) bool {
	if len(grid) != 7 {
		return false
	}
	for _, row := range grid {
		if len(row) != 7 {
			return false
		}
	}
	return true
}

// handleEngineWS is the HTTP entry point for /engine.
func (c *Controller) handleEngineWS(w http.ResponseWriter, r *http.Request) {
	ws, err := c.wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(engineMaxMessageBytes)

	// 1. hello
	ws.SetReadDeadline(time.Now().Add(engineHelloTimeout))
	var hello engineHello
	if err := ws.ReadJSON(&hello); err != nil || hello.Type != "hello" {
		ws.WriteJSON(ctrlSimple{Type: "error", Reason: "expected hello"})
		ws.Close()
		return
	}
	if hello.Version != engineProtocolVersion {
		ws.WriteJSON(ctrlSimple{Type: "error", Reason: "unsupported protocol version; please upgrade your engine"})
		ws.Close()
		return
	}
	if !engineIDPattern.MatchString(hello.EngineID) {
		ws.WriteJSON(ctrlSimple{Type: "error", Reason: "invalid engineId"})
		ws.Close()
		return
	}

	conn := newEngineConn(ws, hello.EngineID)
	go conn.writeLoop()
	clientIP, public := clientIdentity(r)
	endpoint := "ws://" + conn.remote
	if public {
		endpoint = "ws://public" // never publish a participant's IP
	}

	// 2. register on the processor goroutine
	responseChan := make(chan RegisterResponse, 1)
	atomic.AddInt64(&c.registerQueueSize, 1)
	c.registerChan <- &RegisterMessage{
		Request: RegisterRequest{
			PodID:       hello.EngineID,
			DisplayName: sanitiseDisplayName(hello.DisplayName),
			Endpoint:    endpoint,
			Conn:        conn,
			ClientIP:    clientIP,
			Public:      public,
		},
		Response: responseChan,
	}
	reg := <-responseChan
	if reg.Position < 0 {
		reason := reg.Reason
		if reason == "" {
			reason = "grid is full"
		}
		log.Printf("Refused engine %s from %s: %s", hello.EngineID, clientIP, reason)
		conn.enqueue(ctrlSimple{Type: "error", Reason: reason})
		time.Sleep(200 * time.Millisecond)
		conn.close()
		return
	}
	conn.enqueue(ctrlAssigned{
		Type: "assigned", Position: reg.Position,
		Row: reg.Position / gridCols, Col: reg.Position % gridCols,
		Generation: reg.Generation,
	})

	// 3. read loop: state updates until the socket dies
	ws.SetReadDeadline(time.Now().Add(engineReadTimeout))
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(engineReadTimeout))
		return nil
	})
	windowStart := time.Now()
	windowCount := 0
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		ws.SetReadDeadline(time.Now().Add(engineReadTimeout))
		// crude per-connection rate cap: drop, don't disconnect
		if now := time.Now(); now.Sub(windowStart) >= time.Second {
			windowStart, windowCount = now, 0
		}
		windowCount++
		if windowCount > maxStateMsgsPerSecond {
			continue
		}
		var msg engineStateMsg
		if err := json.Unmarshal(data, &msg); err != nil || msg.Type != "state" {
			continue
		}
		if !validGrid(msg.Grid) {
			log.Printf("Engine %s sent a malformed grid; closing", hello.EngineID)
			break
		}
		errChan := make(chan error, 1)
		atomic.AddInt64(&c.stateUpdateQueueSize, 1)
		select {
		case c.stateUpdateChan <- &StateUpdateMessage{
			Position: reg.Position,
			Conn:     conn,
			Request:  StateUpdateRequest{Grid: msg.Grid, Generation: msg.Generation},
			Response: errChan,
		}:
			<-errChan
		default:
			// processor saturated; drop this update rather than stall the socket
			atomic.AddInt64(&c.stateUpdateQueueSize, -1)
		}
	}

	conn.close()
	c.disconnectChan <- &DisconnectMessage{Position: reg.Position, Conn: conn}
}
