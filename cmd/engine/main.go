// Game of Life engine: owns one 7x7 section of the shared grid.
//
// The engine opens a single WebSocket to the controller and never listens on
// anything, so it runs unchanged on a compose network, a laptop behind NAT, or
// a Raspberry Pi on someone's shelf. Protocol in cmd/controller/engine_ws.go.
//
//	CONTROLLER_URL  ws(s) URL of the controller's /engine endpoint
//	DISPLAY_NAME    optional name shown on the public page
//	ENGINE_ID       optional stable id (default: hostname)
package main

import (
	"encoding/json"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ticktockbent/game_of_life/pkg/gameoflife"
)

const (
	protocolVersion = 1
	readTimeout     = 90 * time.Second // controller pings every 30s
	writeTimeout    = 5 * time.Second
	reconnectMin    = 1 * time.Second
	reconnectMax    = 30 * time.Second
	defaultControl  = "wss://gameoflife-api.wshoffner.dev/engine"
)

type Engine struct {
	grid        *gameoflife.Grid
	engineID    string
	displayName string
	controlURL  string
	position    int
	generation  int64 // controller generation we last stepped to
}

// Wire types (mirror cmd/controller/engine_ws.go)
type helloMsg struct {
	Type        string `json:"type"`
	EngineID    string `json:"engineId"`
	DisplayName string `json:"displayName"`
	Version     int    `json:"version"`
}

type stateMsg struct {
	Type       string   `json:"type"`
	Generation int64    `json:"generation"`
	Grid       [][]bool `json:"grid"`
}

type controlMsg struct {
	Type       string     `json:"type"`
	Position   int        `json:"position"`
	Row        int        `json:"row"`
	Col        int        `json:"col"`
	Generation int64      `json:"generation"`
	Halo       [9][9]bool `json:"halo"`
	Reason     string     `json:"reason"`
}

func NewEngine() *Engine {
	engineID := os.Getenv("ENGINE_ID")
	if engineID == "" {
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			engineID = hostname
		} else {
			engineID = "engine"
		}
	}
	controlURL := os.Getenv("CONTROLLER_URL")
	if controlURL == "" {
		controlURL = defaultControl
	}
	// Accept a bare http(s) base URL too, for convenience.
	controlURL = strings.Replace(controlURL, "http://", "ws://", 1)
	controlURL = strings.Replace(controlURL, "https://", "wss://", 1)
	if !strings.HasSuffix(controlURL, "/engine") {
		controlURL = strings.TrimRight(controlURL, "/") + "/engine"
	}

	grid := gameoflife.NewGrid()
	grid.SeedPattern()

	return &Engine{
		grid:        grid,
		engineID:    engineID,
		displayName: os.Getenv("DISPLAY_NAME"),
		controlURL:  controlURL,
		position:    -1,
	}
}

// Run connects, serves one session, and reconnects with backoff forever.
// The grid survives reconnects, so a network blip doesn't reset the section.
func (e *Engine) Run() {
	backoff := reconnectMin
	for {
		err := e.session()
		log.Printf("Session ended: %v — reconnecting in %v", err, backoff)
		time.Sleep(backoff + time.Duration(rand.Int63n(int64(backoff/2))))
		backoff *= 2
		if backoff > reconnectMax {
			backoff = reconnectMax
		}
	}
}

func (e *Engine) session() error {
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	ws, _, err := dialer.Dial(e.controlURL, nil)
	if err != nil {
		return err
	}
	defer ws.Close()

	if err := e.write(ws, helloMsg{Type: "hello", EngineID: e.engineID, DisplayName: e.displayName, Version: protocolVersion}); err != nil {
		return err
	}

	ws.SetReadDeadline(time.Now().Add(readTimeout))
	ws.SetPingHandler(func(data string) error {
		ws.SetReadDeadline(time.Now().Add(readTimeout))
		ws.SetWriteDeadline(time.Now().Add(writeTimeout))
		return ws.WriteMessage(websocket.PongMessage, []byte(data))
	})

	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		ws.SetReadDeadline(time.Now().Add(readTimeout))

		var msg controlMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "assigned":
			e.position = msg.Position
			e.generation = msg.Generation
			log.Printf("Assigned position %d (row %d, col %d) at generation %d", msg.Position, msg.Row, msg.Col, msg.Generation)
			// Show our current section straight away and count as ready.
			if err := e.sendState(ws); err != nil {
				return err
			}

		case "step":
			e.grid.SetHalo(msg.Halo)
			e.grid.NextGeneration()
			e.generation = msg.Generation
			if err := e.sendState(ws); err != nil {
				return err
			}

		case "reseed":
			e.grid.SeedPattern()
			if err := e.sendState(ws); err != nil {
				return err
			}

		case "error":
			log.Printf("Controller refused us: %s", msg.Reason)
			// Back off hard for protocol errors so we don't hammer a controller
			// that will keep saying no.
			time.Sleep(reconnectMax)
			return nil
		}
	}
}

func (e *Engine) sendState(ws *websocket.Conn) error {
	cells := make([][]bool, gameoflife.GridSize)
	for i := range cells {
		cells[i] = make([]bool, gameoflife.GridSize)
		for j := range cells[i] {
			cells[i][j] = bool(e.grid.Cells[i][j])
		}
	}
	return e.write(ws, stateMsg{Type: "state", Generation: e.generation, Grid: cells})
}

func (e *Engine) write(ws *websocket.Conn, v interface{}) error {
	ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	return ws.WriteJSON(v)
}

func main() {
	engine := NewEngine()
	log.Printf("Engine %s starting → %s", engine.engineID, engine.controlURL)
	engine.Run()
}
