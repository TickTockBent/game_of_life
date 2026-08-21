package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

// Controller with channel-based message handling and zero locks
type Controller struct {
	// Message channels for different request types
	registerChan     chan *RegisterMessage
	stateUpdateChan  chan *StateUpdateMessage
	webReadChan      chan *WebReadMessage
	clickChan        chan *ClickMessage
	disconnectChan   chan *DisconnectMessage
	manualStepChan   chan struct{}
	randomizeAllChan chan *RandomizeAllMessage

	// State (only accessed by the main goroutine)
	nodes             map[int]*NodeInfo
	gridStates        map[int]*GridState
	currentGeneration int64
	regionID          string

	// Barrier sync state
	readyEngines   map[int]bool  // tracks which engines are ready for current generation
	barrierTimeout time.Duration // timeout for waiting for all engines
	stepInterval   time.Duration // minimum time between steps (fixed tick)
	lagThreshold   int           // missed steps before an engine is flagged lagging
	staleThreshold int           // missed steps before an engine is removed
	slotOrder      []int         // slot assignment order (centre-out spiral)

	// Debug controls
	steppingPaused bool // When true, barrier coordinator won't auto-step

	// WebSocket clients for real-time updates
	wsClients  map[*websocket.Conn]bool // connected WebSocket clients
	wsMutex    sync.RWMutex             // protects wsClients map
	wsUpgrader websocket.Upgrader       // WebSocket upgrader

	// Metrics (atomic counters)
	registerQueueSize      int64
	stateUpdateQueueSize   int64
	haloReadQueueSize      int64
	webReadQueueSize       int64
	stepBroadcastQueueSize int64
}

// Message types for channels
type RegisterMessage struct {
	Request  RegisterRequest
	Response chan RegisterResponse
}

type StateUpdateMessage struct {
	Position int
	Conn     *engineConn // socket the update arrived on; stale sockets are ignored
	Request  StateUpdateRequest
	Response chan error
}

type DisconnectMessage struct {
	Position int
	Conn     *engineConn
}

type WebReadMessage struct {
	Type     string // "generation", "aggregated-state", "topology", "health"
	Response chan interface{}
}

type ClickMessage struct {
	Request  ClickRequest
	Response chan error
}

type RandomizeAllMessage struct {
	Response chan int
}

// Data types
type NodeInfo struct {
	PodID         string      `json:"podId"`
	DisplayName   string      `json:"displayName,omitempty"` // Optional user-friendly name
	Position      Position    `json:"position"`
	Endpoint      string      `json:"endpoint"`
	RegisteredAt  time.Time   `json:"registeredAt"`
	LastHeartbeat time.Time   `json:"lastHeartbeat"`
	MissedSteps   int         `json:"missedSteps"` // Count of consecutive missed state pushes
	Lagging       bool        `json:"lagging"`     // True when the engine has missed recent steps; its section is stale
	Connected     bool        `json:"connected"`   // False while the engine's socket is down (slot is held for it)
	Conn          *engineConn `json:"-"`
}

type Position struct {
	Row int `json:"row"`
	Col int `json:"col"`
}

type GridState struct {
	Grid       [][]bool  `json:"grid"`
	Generation int       `json:"generation"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type RegisterRequest struct {
	PodID       string      `json:"podId"`
	Endpoint    string      `json:"endpoint"`
	DisplayName string      `json:"displayName,omitempty"` // Optional user-friendly name
	Conn        *engineConn `json:"-"`
}

type RegisterResponse struct {
	Position   int   `json:"position"`
	Generation int64 `json:"generation"`
}

type StateUpdateRequest struct {
	Grid       [][]bool `json:"grid"`
	Generation int      `json:"generation"`
}

type ClickRequest struct {
	GlobalX int  `json:"globalX"`
	GlobalY int  `json:"globalY"`
	Alive   bool `json:"alive"`
}

type TopologyResponse struct {
	RegionID string            `json:"regionId"`
	Nodes    map[int]*NodeInfo `json:"nodes"`
}

type AggregatedStateResponse struct {
	Topology *TopologyResponse     `json:"topology"`
	Grids    map[string]*GridState `json:"grids"`
}

type HaloResponse struct {
	HaloCells [9][9]bool `json:"haloCells"`
}

const (
	gridCols = 10
	gridRows = 10
)

func durationFromEnv(key string, fallback time.Duration) time.Duration {
	if raw := os.Getenv(key); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
		log.Printf("Ignoring invalid %s=%q, using %v", key, raw, fallback)
	}
	return fallback
}

func intFromEnv(key string, fallback int) int {
	if raw := os.Getenv(key); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
		log.Printf("Ignoring invalid %s=%q, using %d", key, raw, fallback)
	}
	return fallback
}

// spiralSlotOrder returns every slot index in the layout sorted by distance
// from the centre, so newly joining engines fill a compact blob that grows
// outward rather than a strip along the top row.
func spiralSlotOrder(cols, rows int) []int {
	centreX := float64(cols-1) / 2
	centreY := float64(rows-1) / 2
	order := make([]int, 0, cols*rows)
	for slot := 0; slot < cols*rows; slot++ {
		order = append(order, slot)
	}
	distance := func(slot int) float64 {
		dx := float64(slot%cols) - centreX
		dy := float64(slot/cols) - centreY
		return dx*dx + dy*dy
	}
	angle := func(slot int) float64 {
		dx := float64(slot%cols) - centreX
		dy := float64(slot/cols) - centreY
		return math.Atan2(dy, dx)
	}
	sort.SliceStable(order, func(i, j int) bool {
		di, dj := distance(order[i]), distance(order[j])
		if di != dj {
			return di < dj
		}
		return angle(order[i]) < angle(order[j])
	})
	return order
}

func NewController() *Controller {
	regionID := os.Getenv("REGION_ID")
	if regionID == "" {
		regionID = "local"
	}

	c := &Controller{
		registerChan:      make(chan *RegisterMessage, 200),
		stateUpdateChan:   make(chan *StateUpdateMessage, 2000),
		webReadChan:       make(chan *WebReadMessage, 1000),
		clickChan:         make(chan *ClickMessage, 100),
		disconnectChan:    make(chan *DisconnectMessage, 200),
		manualStepChan:    make(chan struct{}, 1),
		randomizeAllChan:  make(chan *RandomizeAllMessage, 10),
		nodes:             make(map[int]*NodeInfo),
		gridStates:        make(map[int]*GridState),
		currentGeneration: 0,
		regionID:          regionID,
		readyEngines:      make(map[int]bool),
		barrierTimeout:    durationFromEnv("BARRIER_TIMEOUT", 1000*time.Millisecond),
		stepInterval:      durationFromEnv("STEP_INTERVAL", 250*time.Millisecond),
		lagThreshold:      intFromEnv("LAG_THRESHOLD", 2),
		staleThreshold:    intFromEnv("STALE_THRESHOLD", 40),
		slotOrder:         spiralSlotOrder(gridCols, gridRows),
		steppingPaused:    false,
		wsClients:         make(map[*websocket.Conn]bool),
		wsUpgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for development
			},
		},
	}

	// Start the main message processor
	go c.messageProcessor()

	// Start health check timer
	go c.healthChecker()

	return c
}

// Main message processing loop - all state mutations happen here
func (c *Controller) messageProcessor() {
	barrierTicker := time.NewTicker(25 * time.Millisecond)
	defer barrierTicker.Stop()
	lastStep := time.Now()

	for {
		select {
		case <-barrierTicker.C:
			if c.shouldStep(time.Since(lastStep)) {
				lastStep = time.Now()
				c.broadcastStepToAllEngines()
			}

		case <-c.manualStepChan:
			lastStep = time.Now()
			c.broadcastStepToAllEngines()

		case msg := <-c.disconnectChan:
			c.processDisconnect(msg.Position, msg.Conn)

		case msg := <-c.registerChan:
			atomic.AddInt64(&c.registerQueueSize, -1)
			response := c.processRegister(msg.Request)
			msg.Response <- response

		case msg := <-c.stateUpdateChan:
			atomic.AddInt64(&c.stateUpdateQueueSize, -1)
			err := c.processStateUpdate(msg.Position, msg.Conn, msg.Request)
			msg.Response <- err

		case msg := <-c.webReadChan:
			atomic.AddInt64(&c.webReadQueueSize, -1)
			response := c.processWebRead(msg.Type)
			msg.Response <- response

		case msg := <-c.clickChan:
			err := c.processClick(msg.Request)
			msg.Response <- err

		case msg := <-c.randomizeAllChan:
			success := c.processRandomizeAll()
			msg.Response <- success
		}
	}
}

// Process register request
func (c *Controller) processRegister(req RegisterRequest) RegisterResponse {
	// Check if this pod is already registered
	for pos, node := range c.nodes {
		if node.PodID == req.PodID {
			// Same engine reconnecting (or a duplicate): the newest socket wins,
			// the slot is kept.
			if node.Conn != nil && node.Conn != req.Conn {
				node.Conn.close()
			}
			node.Conn = req.Conn
			node.Connected = req.Conn != nil
			node.Endpoint = req.Endpoint
			if req.DisplayName != "" {
				node.DisplayName = req.DisplayName
			}
			node.LastHeartbeat = time.Now()
			log.Printf("Engine %s reconnected at position %d", req.PodID, pos)
			return RegisterResponse{Position: pos, Generation: atomic.LoadInt64(&c.currentGeneration)}
		}
	}

	// Find the free slot nearest the centre of the layout
	position := -1
	for _, slot := range c.slotOrder {
		if _, exists := c.nodes[slot]; !exists {
			position = slot
			break
		}
	}

	if position == -1 {
		return RegisterResponse{Position: -1} // Will handle error in handler
	}

	// Convert position to row/col (10x10 grid layout)
	row := position / gridCols
	col := position % gridCols

	now := time.Now()
	c.nodes[position] = &NodeInfo{
		PodID:         req.PodID,
		DisplayName:   req.DisplayName, // Store display name if provided
		Position:      Position{Row: row, Col: col},
		Endpoint:      req.Endpoint,
		RegisteredAt:  now,
		LastHeartbeat: now,
		Conn:          req.Conn,
		Connected:     req.Conn != nil,
	}

	log.Printf("Registered engine %s at position %d", req.PodID, position)
	return RegisterResponse{Position: position, Generation: atomic.LoadInt64(&c.currentGeneration)}
}

// Process state update
func (c *Controller) processStateUpdate(position int, conn *engineConn, req StateUpdateRequest) error {
	node, exists := c.nodes[position]
	if !exists || (conn != nil && node.Conn != conn) {
		return fmt.Errorf("stale connection for position %d", position)
	}
	// Update grid state
	c.gridStates[position] = &GridState{
		Grid:       req.Grid,
		Generation: req.Generation,
		UpdatedAt:  time.Now(),
	}

	// Update heartbeat for this node
	if node, exists := c.nodes[position]; exists {
		node.LastHeartbeat = time.Now()
	}

	// Mark engine as ready for current generation (state post = readiness signal)
	if req.Generation == int(c.currentGeneration) {
		c.readyEngines[position] = true
		log.Printf("Engine %d ready for generation %d (%d/%d ready)",
			position, c.currentGeneration, len(c.readyEngines), len(c.nodes))
	}

	return nil
}

// processDisconnect marks an engine's socket as gone. The slot is kept; the
// miss budget removes the engine if it doesn't come back.
func (c *Controller) processDisconnect(position int, conn *engineConn) {
	node, exists := c.nodes[position]
	if !exists || node.Conn != conn {
		return // a newer socket already replaced this one
	}
	node.Conn = nil
	node.Connected = false
	log.Printf("Engine %s at position %d disconnected; holding slot", node.PodID, position)
}

// Process halo read request
func (c *Controller) processHaloRead(position int) HaloResponse {
	// Calculate grid coordinates (10x10 layout)
	row := position / 10
	col := position % 10

	var halo [9][9]bool

	// Build 9x9 halo with this position's 7x7 in center
	for haloRow := 0; haloRow < 9; haloRow++ {
		for haloCol := 0; haloCol < 9; haloCol++ {
			// Center 7x7 comes from our own grid
			if haloRow >= 1 && haloRow <= 7 && haloCol >= 1 && haloCol <= 7 {
				gridRow := haloRow - 1
				gridCol := haloCol - 1
				if state, exists := c.gridStates[position]; exists &&
					gridRow < len(state.Grid) && gridCol < len(state.Grid[gridRow]) {
					halo[haloRow][haloCol] = state.Grid[gridRow][gridCol]
				}
			} else {
				// Edge cells come from neighbors - determine which neighbor and cell
				var neighborPos int = -1
				var nRow, nCol int

				if haloRow == 0 && haloCol >= 1 && haloCol <= 7 {
					// North edge - neighbor is position - 10
					if row > 0 {
						neighborPos = (row-1)*10 + col
						nRow = 6           // Get neighbor's south edge
						nCol = haloCol - 1 // Map halo col 1-7 to grid col 0-6
					}
				} else if haloRow == 8 && haloCol >= 1 && haloCol <= 7 {
					// South edge - neighbor is position + 10
					if row < 9 {
						neighborPos = (row+1)*10 + col
						nRow = 0 // Get neighbor's north edge
						nCol = haloCol - 1
					}
				} else if haloCol == 0 && haloRow >= 1 && haloRow <= 7 {
					// West edge - neighbor is position - 1
					if col > 0 {
						neighborPos = row*10 + (col - 1)
						nRow = haloRow - 1 // Map halo row 1-7 to grid row 0-6
						nCol = 6           // Get neighbor's east edge
					}
				} else if haloCol == 8 && haloRow >= 1 && haloRow <= 7 {
					// East edge - neighbor is position + 1
					if col < 9 {
						neighborPos = row*10 + (col + 1)
						nRow = haloRow - 1
						nCol = 0 // Get neighbor's west edge
					}
				} else {
					// Corner: diagonal neighbour's opposite corner cell
					dRow, dCol := -1, -1
					if haloRow == 8 {
						dRow = 1
					}
					if haloCol == 8 {
						dCol = 1
					}
					if row+dRow >= 0 && row+dRow < gridRows && col+dCol >= 0 && col+dCol < gridCols {
						neighborPos = (row+dRow)*gridCols + (col + dCol)
						nRow, nCol = 0, 0
						if dRow < 0 {
							nRow = 6
						}
						if dCol < 0 {
							nCol = 6
						}
					}
				}

				// Get the cell from the neighbor if valid
				if neighborPos >= 0 {
					if state, exists := c.gridStates[neighborPos]; exists {
						if nRow >= 0 && nRow < 7 && nCol >= 0 && nCol < 7 &&
							nRow < len(state.Grid) && nCol < len(state.Grid[nRow]) {
							halo[haloRow][haloCol] = state.Grid[nRow][nCol]
						}
					}
				}
			}
		}
	}

	return HaloResponse{HaloCells: halo}
}

// Process web read requests
func (c *Controller) processWebRead(requestType string) interface{} {
	switch requestType {
	case "generation":
		return map[string]interface{}{
			"generation": atomic.LoadInt64(&c.currentGeneration),
		}

	case "aggregated-state":
		// Copy node data
		nodes := make(map[int]*NodeInfo)
		for k, v := range c.nodes {
			nodes[k] = v
		}

		// Copy grid state data
		grids := make(map[string]*GridState)
		for position, state := range c.gridStates {
			grids[strconv.Itoa(position)] = state
		}

		return &AggregatedStateResponse{
			Topology: &TopologyResponse{
				RegionID: c.regionID,
				Nodes:    nodes,
			},
			Grids: grids,
		}

	case "topology":
		nodes := make(map[int]*NodeInfo)
		for k, v := range c.nodes {
			nodes[k] = v
		}

		return TopologyResponse{
			RegionID: c.regionID,
			Nodes:    nodes,
		}

	case "health":
		return map[string]interface{}{
			"status":      "healthy",
			"regionId":    c.regionID,
			"nodes":       len(c.nodes),
			"activeGrids": len(c.gridStates),
			"generation":  atomic.LoadInt64(&c.currentGeneration),
			"queues": map[string]int64{
				"register":      atomic.LoadInt64(&c.registerQueueSize),
				"stateUpdate":   atomic.LoadInt64(&c.stateUpdateQueueSize),
				"haloRead":      atomic.LoadInt64(&c.haloReadQueueSize),
				"webRead":       atomic.LoadInt64(&c.webReadQueueSize),
				"stepBroadcast": atomic.LoadInt64(&c.stepBroadcastQueueSize),
			},
		}

	case "metrics":
		// Metrics for web interface
		webQueue := atomic.LoadInt64(&c.webReadQueueSize)
		if webQueue < 0 {
			webQueue = 0 // Fix negative queue bug
		}

		return map[string]interface{}{
			"timestamp":   time.Now().Unix(),
			"regionId":    c.regionID,
			"generation":  atomic.LoadInt64(&c.currentGeneration),
			"nodes":       len(c.nodes),
			"activeGrids": len(c.gridStates),
			"totalQueueSize": atomic.LoadInt64(&c.registerQueueSize) +
				atomic.LoadInt64(&c.stateUpdateQueueSize) +
				atomic.LoadInt64(&c.haloReadQueueSize) +
				webQueue +
				atomic.LoadInt64(&c.stepBroadcastQueueSize),
			"regQueueSize":   atomic.LoadInt64(&c.registerQueueSize),
			"stateQueueSize": atomic.LoadInt64(&c.stateUpdateQueueSize),
			"haloQueueSize":  atomic.LoadInt64(&c.haloReadQueueSize),
			"webQueueSize":   webQueue,
			"stepQueueSize":  atomic.LoadInt64(&c.stepBroadcastQueueSize),
		}

	default:
		return nil
	}
}

// Process click request: sends a randomize signal to the engine owning the
// clicked grid section. Runs on the message processor goroutine, so c.nodes
// is accessed safely here. The HTTP call is dispatched in a goroutine to
// avoid blocking the message processor.
func (c *Controller) processClick(req ClickRequest) error {
	// Calculate which grid this click belongs to (7x7 grids, 10x10 layout)
	gridX := req.GlobalX / 7
	gridY := req.GlobalY / 7
	position := gridY*10 + gridX

	node, exists := c.nodes[position]
	if !exists {
		return fmt.Errorf("no engine registered at grid position (%d, %d)", gridX, gridY)
	}

	if node.Conn == nil {
		return fmt.Errorf("engine at position %d is disconnected", position)
	}
	log.Printf("Click at (%d,%d): reseeding position %d (engine %s)", req.GlobalX, req.GlobalY, position, node.PodID)
	node.Conn.enqueue(ctrlSimple{Type: "reseed"})
	return nil
}

// processRandomizeAll sends a POST /randomize to every registered engine.
// Runs on the message processor goroutine; HTTP fan-out is dispatched in
// goroutines so the processor isn't blocked.
func (c *Controller) processRandomizeAll() int {
	count := 0
	for _, node := range c.nodes {
		if node.Conn != nil && node.Conn.enqueue(ctrlSimple{Type: "reseed"}) {
			count++
		}
	}
	return count
}

// broadcastStepToAllEngines advances the generation and sends each connected
// engine its step message with the full 9x9 halo. Runs on the processor
// goroutine, so reading nodes/gridStates here is safe.
func (c *Controller) broadcastStepToAllEngines() {
	atomic.AddInt64(&c.currentGeneration, 1)
	newGeneration := atomic.LoadInt64(&c.currentGeneration)

	c.updateMissedSteps()

	for position, node := range c.nodes {
		if node.Conn == nil {
			continue
		}
		halo := c.processHaloRead(position).HaloCells
		node.Conn.enqueue(ctrlStep{Type: "step", Generation: newGeneration, Halo: halo})
	}

	c.cleanupStaleEngines()
	c.broadcastToWebSocketClients()
	c.readyEngines = make(map[int]bool)
}

// updateMissedSteps increments missed step count for engines that didn't report ready
func (c *Controller) updateMissedSteps() {
	for position, node := range c.nodes {
		if c.readyEngines[position] {
			// Engine was ready, reset missed steps
			if node.Lagging {
				log.Printf("Engine %s at position %d caught up", node.PodID, position)
			}
			node.MissedSteps = 0
			node.Lagging = false
		} else {
			// Engine missed this step; its section is soft-skipped (frozen) until it catches up
			node.MissedSteps++
			if !node.Lagging && node.MissedSteps >= c.lagThreshold {
				log.Printf("Engine %s at position %d is lagging (missed %d steps)", node.PodID, position, node.MissedSteps)
				node.Lagging = true
			}
		}
	}
}

// cleanupStaleEngines removes engines that have missed too many steps
func (c *Controller) cleanupStaleEngines() {
	for position, node := range c.nodes {
		if node.MissedSteps >= c.staleThreshold {
			log.Printf("Removing stale engine %s at position %d (missed %d steps)",
				node.PodID, position, node.MissedSteps)
			if node.Conn != nil {
				node.Conn.close()
			}
			delete(c.nodes, position)
			delete(c.gridStates, position)
		}
	}
}

// broadcastToWebSocketClients sends current aggregated state to all connected WebSocket clients
func (c *Controller) broadcastToWebSocketClients() {
	// Get current aggregated state
	nodes := make(map[int]*NodeInfo)
	for k, v := range c.nodes {
		nodes[k] = v
	}

	grids := make(map[string]*GridState)
	for position, state := range c.gridStates {
		grids[strconv.Itoa(position)] = state
	}

	aggregatedState := &AggregatedStateResponse{
		Topology: &TopologyResponse{
			RegionID: c.regionID,
			Nodes:    nodes,
		},
		Grids: grids,
	}

	// Convert to JSON
	jsonData, err := json.Marshal(aggregatedState)
	if err != nil {
		log.Printf("Failed to marshal WebSocket data: %v", err)
		return
	}

	// Broadcast to all connected clients
	c.wsMutex.RLock()
	defer c.wsMutex.RUnlock()

	for client := range c.wsClients {
		err := client.WriteMessage(websocket.TextMessage, jsonData)
		if err != nil {
			log.Printf("WebSocket write error: %v", err)
			// Remove disconnected client
			go c.removeWebSocketClient(client)
		}
	}
}

// removeWebSocketClient safely removes a disconnected client
func (c *Controller) removeWebSocketClient(client *websocket.Conn) {
	c.wsMutex.Lock()
	defer c.wsMutex.Unlock()

	if _, exists := c.wsClients[client]; exists {
		delete(c.wsClients, client)
		client.Close()
		log.Printf("Removed disconnected WebSocket client")
	}
}

// shouldStep is the barrier decision: step when every connected, non-lagging
// engine has reported for this generation and the fixed tick has elapsed, or
// when the barrier timeout expires regardless.
func (c *Controller) shouldStep(elapsed time.Duration) bool {
	if c.steppingPaused || len(c.nodes) == 0 {
		return false
	}
	expected := 0
	for position, node := range c.nodes {
		if node.Conn == nil && !c.readyEngines[position] {
			continue // disconnected engines never hold the barrier
		}
		if node.Lagging && !c.readyEngines[position] {
			continue
		}
		expected++
	}
	allReady := len(c.readyEngines) >= expected
	return (allReady && elapsed >= c.stepInterval) || elapsed >= c.barrierTimeout
}

// healthChecker sends periodic health check messages
func (c *Controller) healthChecker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Send a health check message to clean up stale nodes
		responseChan := make(chan interface{}, 1)
		select {
		case c.webReadChan <- &WebReadMessage{Type: "health-check", Response: responseChan}:
			// Message queued successfully
		default:
			// Queue full, skip this health check
		}
	}
}

// HTTP Handlers - these just queue messages and wait for responses

// GET /generation
func (c *Controller) handleGeneration(w http.ResponseWriter, r *http.Request) {
	responseChan := make(chan interface{}, 1)
	msg := &WebReadMessage{
		Type:     "generation",
		Response: responseChan,
	}

	// Try to queue the message
	select {
	case c.webReadChan <- msg:
		atomic.AddInt64(&c.webReadQueueSize, 1)
		// Wait for response
		response := <-responseChan
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	default:
		// Queue full
		http.Error(w, "Controller overloaded", http.StatusServiceUnavailable)
	}
}

// GET /aggregated-state
func (c *Controller) handleAggregatedState(w http.ResponseWriter, r *http.Request) {
	responseChan := make(chan interface{}, 1)
	msg := &WebReadMessage{
		Type:     "aggregated-state",
		Response: responseChan,
	}

	// Try to queue the message
	select {
	case c.webReadChan <- msg:
		atomic.AddInt64(&c.webReadQueueSize, 1)
		// Wait for response
		response := <-responseChan
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	default:
		// Queue full
		http.Error(w, "Controller overloaded", http.StatusServiceUnavailable)
	}
}

// GET /topology
func (c *Controller) handleTopology(w http.ResponseWriter, r *http.Request) {
	responseChan := make(chan interface{}, 1)
	msg := &WebReadMessage{
		Type:     "topology",
		Response: responseChan,
	}

	// Try to queue the message
	select {
	case c.webReadChan <- msg:
		atomic.AddInt64(&c.webReadQueueSize, 1)
		// Wait for response
		response := <-responseChan
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	default:
		// Queue full
		http.Error(w, "Controller overloaded", http.StatusServiceUnavailable)
	}
}

// POST /api/click
func (c *Controller) handleClick(w http.ResponseWriter, r *http.Request) {
	var req ClickRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	responseChan := make(chan error, 1)
	msg := &ClickMessage{
		Request:  req,
		Response: responseChan,
	}

	// Try to queue the message
	select {
	case c.clickChan <- msg:
		// Wait for response
		if err := <-responseChan; err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		// Queue full
		http.Error(w, "Controller overloaded", http.StatusServiceUnavailable)
	}
}

// POST /api/randomize - sends a randomize request to every registered engine.
// This dispatches through the message processor via a dedicated channel so
// that c.nodes is read safely. The HTTP fan-out to engines happens in
// goroutines to avoid blocking the message processor.
func (c *Controller) handleRandomize(w http.ResponseWriter, r *http.Request) {
	responseChan := make(chan int, 1)
	msg := &RandomizeAllMessage{Response: responseChan}

	select {
	case c.randomizeAllChan <- msg:
		success := <-responseChan
		response := map[string]interface{}{
			"success": success,
			"total":   success, // total == success since we attempt all registered nodes
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	default:
		http.Error(w, "Controller overloaded", http.StatusServiceUnavailable)
	}
}

// GET /health
func (c *Controller) handleHealth(w http.ResponseWriter, r *http.Request) {
	responseChan := make(chan interface{}, 1)
	msg := &WebReadMessage{
		Type:     "health",
		Response: responseChan,
	}

	// Try to queue the message
	select {
	case c.webReadChan <- msg:
		atomic.AddInt64(&c.webReadQueueSize, 1)
		// Wait for response
		response := <-responseChan
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	default:
		// Queue full
		http.Error(w, "Controller overloaded", http.StatusServiceUnavailable)
	}
}

// Legacy API mapping for web interface
func (c *Controller) handleApiGrid(w http.ResponseWriter, r *http.Request) {
	c.handleAggregatedState(w, r)
}

// GET /metrics
func (c *Controller) handleMetrics(w http.ResponseWriter, r *http.Request) {
	responseChan := make(chan interface{}, 1)
	msg := &WebReadMessage{
		Type:     "metrics",
		Response: responseChan,
	}

	// Try to queue the message
	select {
	case c.webReadChan <- msg:
		atomic.AddInt64(&c.webReadQueueSize, 1)
		// Wait for response
		response := <-responseChan
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	default:
		// Queue full
		http.Error(w, "Controller overloaded", http.StatusServiceUnavailable)
	}
}

// handleWebSocket upgrades HTTP connections to WebSocket for real-time updates
func (c *Controller) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := c.wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	// Add client to active connections
	c.wsMutex.Lock()
	c.wsClients[conn] = true
	clientCount := len(c.wsClients)
	c.wsMutex.Unlock()

	log.Printf("New WebSocket client connected (total: %d)", clientCount)

	// Send initial state immediately
	c.sendInitialState(conn)

	// Handle client disconnection
	defer func() {
		c.removeWebSocketClient(conn)
	}()

	// Keep connection alive by reading ping/pong messages
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			log.Printf("WebSocket read error: %v", err)
			break
		}
	}
}

// Debug endpoints

// POST /debug/pause
func (c *Controller) handleDebugPause(w http.ResponseWriter, r *http.Request) {
	c.steppingPaused = true
	log.Printf("DEBUG: Stepping paused at generation %d", atomic.LoadInt64(&c.currentGeneration))

	response := map[string]interface{}{
		"status":     "paused",
		"generation": atomic.LoadInt64(&c.currentGeneration),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// POST /debug/unpause
func (c *Controller) handleDebugUnpause(w http.ResponseWriter, r *http.Request) {
	c.steppingPaused = false
	log.Printf("DEBUG: Stepping unpaused at generation %d", atomic.LoadInt64(&c.currentGeneration))

	response := map[string]interface{}{
		"status":     "running",
		"generation": atomic.LoadInt64(&c.currentGeneration),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// POST /debug/nextstep
func (c *Controller) handleDebugNextStep(w http.ResponseWriter, r *http.Request) {
	if !c.steppingPaused {
		http.Error(w, "Must be paused to use nextstep", http.StatusBadRequest)
		return
	}
	select {
	case c.manualStepChan <- struct{}{}:
	default:
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "step_triggered",
		"generation": atomic.LoadInt64(&c.currentGeneration) + 1,
	})
}

// sendInitialState sends current aggregated state to a new WebSocket client
func (c *Controller) sendInitialState(conn *websocket.Conn) {
	// Get current aggregated state
	nodes := make(map[int]*NodeInfo)
	for k, v := range c.nodes {
		nodes[k] = v
	}

	grids := make(map[string]*GridState)
	for position, state := range c.gridStates {
		grids[strconv.Itoa(position)] = state
	}

	aggregatedState := &AggregatedStateResponse{
		Topology: &TopologyResponse{
			RegionID: c.regionID,
			Nodes:    nodes,
		},
		Grids: grids,
	}

	// Convert to JSON and send
	jsonData, err := json.Marshal(aggregatedState)
	if err != nil {
		log.Printf("Failed to marshal initial WebSocket data: %v", err)
		return
	}

	err = conn.WriteMessage(websocket.TextMessage, jsonData)
	if err != nil {
		log.Printf("Failed to send initial WebSocket data: %v", err)
	}
}

func main() {
	controller := NewController()

	r := mux.NewRouter()

	// Core endpoints
	r.HandleFunc("/engine", controller.handleEngineWS) // engines connect here (WebSocket)
	r.HandleFunc("/generation", controller.handleGeneration).Methods("GET")
	r.HandleFunc("/aggregated-state", controller.handleAggregatedState).Methods("GET")
	r.HandleFunc("/topology", controller.handleTopology).Methods("GET")
	r.HandleFunc("/api/click", controller.handleClick).Methods("POST")
	r.HandleFunc("/health", controller.handleHealth).Methods("GET")

	// Legacy API mapping
	r.HandleFunc("/api/grid", controller.handleApiGrid).Methods("GET")

	// Metrics endpoint for web interface
	r.HandleFunc("/metrics", controller.handleMetrics).Methods("GET")

	// WebSocket endpoint for real-time updates
	r.HandleFunc("/ws", controller.handleWebSocket)

	// Admin surface: never exposed publicly. Separate listener, separate port.
	admin := mux.NewRouter()
	admin.HandleFunc("/api/randomize", controller.handleRandomize).Methods("POST")
	admin.HandleFunc("/debug/pause", controller.handleDebugPause).Methods("POST")
	admin.HandleFunc("/debug/unpause", controller.handleDebugUnpause).Methods("POST")
	admin.HandleFunc("/debug/nextstep", controller.handleDebugNextStep).Methods("POST")
	admin.HandleFunc("/health", controller.handleHealth).Methods("GET")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	adminPort := os.Getenv("ADMIN_PORT")
	if adminPort == "" {
		adminPort = "8091"
	}
	go func() {
		log.Printf("Admin listener on port %s (/debug/*, /api/randomize)", adminPort)
		log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", adminPort), admin))
	}()

	log.Printf("Game of Life Controller with Channels starting on port %s (Region: %s)", port, controller.regionID)
	log.Printf("Step interval %v, barrier timeout %v, lag after %d missed, remove after %d missed",
		controller.stepInterval, controller.barrierTimeout, controller.lagThreshold, controller.staleThreshold)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), r))
}
