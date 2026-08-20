package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
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
	registerChan    chan *RegisterMessage
	stateUpdateChan chan *StateUpdateMessage
	haloReadChan    chan *HaloReadMessage
	webReadChan     chan *WebReadMessage
	clickChan       chan *ClickMessage
	stepBroadcastChan chan *StepBroadcastMessage
	randomizeAllChan  chan *RandomizeAllMessage
	
	// State (only accessed by the main goroutine)
	nodes             map[int]*NodeInfo
	gridStates        map[int]*GridState
	currentGeneration int64
	regionID          string
	
	// Barrier sync state
	readyEngines      map[int]bool  // tracks which engines are ready for current generation
	stepInProgress    bool          // true when step broadcast is in progress
	barrierTimeout    time.Duration // timeout for waiting for all engines
	
	// Debug controls
	steppingPaused    bool          // When true, barrier coordinator won't auto-step
	
	// WebSocket clients for real-time updates
	wsClients    map[*websocket.Conn]bool // connected WebSocket clients
	wsMutex      sync.RWMutex             // protects wsClients map
	wsUpgrader   websocket.Upgrader       // WebSocket upgrader
	
	// Metrics (atomic counters)
	registerQueueSize    int64
	stateUpdateQueueSize int64
	haloReadQueueSize    int64
	webReadQueueSize     int64
	stepBroadcastQueueSize int64
}

// Message types for channels
type RegisterMessage struct {
	Request  RegisterRequest
	Response chan RegisterResponse
}

type StateUpdateMessage struct {
	Position int
	Request  StateUpdateRequest
	Response chan error
}

type HaloReadMessage struct {
	Position int
	Response chan HaloResponse
}

type WebReadMessage struct {
	Type     string // "generation", "aggregated-state", "topology", "health"
	Response chan interface{}
}

type ClickMessage struct {
	Request  ClickRequest
	Response chan error
}

type StepBroadcastMessage struct {
	Position int
	Response chan error
}

type RandomizeAllMessage struct {
	Response chan int
}

// Data types
type NodeInfo struct {
	PodID         string    `json:"podId"`
	DisplayName   string    `json:"displayName,omitempty"` // Optional user-friendly name
	Position      Position  `json:"position"`
	Endpoint      string    `json:"endpoint"`
	RegisteredAt  time.Time `json:"registeredAt"`
	LastHeartbeat time.Time `json:"lastHeartbeat"`
	MissedSteps   int       `json:"missedSteps"` // Count of consecutive missed state pushes
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
	PodID       string `json:"podId"`
	Endpoint    string `json:"endpoint"`
	DisplayName string `json:"displayName,omitempty"` // Optional user-friendly name
}

type RegisterResponse struct {
	Position int `json:"position"`
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
	RegionID string              `json:"regionId"`
	Nodes    map[int]*NodeInfo   `json:"nodes"`
}

type AggregatedStateResponse struct {
	Topology *TopologyResponse       `json:"topology"`
	Grids    map[string]*GridState   `json:"grids"`
}

type HaloResponse struct {
	HaloCells [9][9]bool `json:"haloCells"`
}

func NewController() *Controller {
	regionID := os.Getenv("REGION_ID")
	if regionID == "" {
		regionID = "k3s-cluster"
	}

	c := &Controller{
		registerChan:      make(chan *RegisterMessage, 200),
		stateUpdateChan:   make(chan *StateUpdateMessage, 2000),
		haloReadChan:      make(chan *HaloReadMessage, 1000),
		webReadChan:       make(chan *WebReadMessage, 1000),
		clickChan:         make(chan *ClickMessage, 100),
		stepBroadcastChan: make(chan *StepBroadcastMessage, 1000),
		randomizeAllChan:  make(chan *RandomizeAllMessage, 10),
		nodes:             make(map[int]*NodeInfo),
		gridStates:        make(map[int]*GridState),
		currentGeneration: 0,
		regionID:          regionID,
		readyEngines:      make(map[int]bool),
		stepInProgress:    false,
		barrierTimeout:    1000 * time.Millisecond, // 1 second barrier timeout
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
	
	// Start barrier coordinator
	go c.barrierCoordinator()
	
	// Start health check timer
	go c.healthChecker()
	
	return c
}

// Main message processing loop - all state mutations happen here
func (c *Controller) messageProcessor() {
	for {
		select {
		case msg := <-c.registerChan:
			atomic.AddInt64(&c.registerQueueSize, -1)
			response := c.processRegister(msg.Request)
			msg.Response <- response
			
		case msg := <-c.stateUpdateChan:
			atomic.AddInt64(&c.stateUpdateQueueSize, -1)
			err := c.processStateUpdate(msg.Position, msg.Request)
			msg.Response <- err
			
		case msg := <-c.haloReadChan:
			atomic.AddInt64(&c.haloReadQueueSize, -1)
			response := c.processHaloRead(msg.Position)
			msg.Response <- response
			
		case msg := <-c.webReadChan:
			atomic.AddInt64(&c.webReadQueueSize, -1)
			response := c.processWebRead(msg.Type)
			msg.Response <- response
			
		case msg := <-c.clickChan:
			err := c.processClick(msg.Request)
			msg.Response <- err
			
		case msg := <-c.stepBroadcastChan:
			atomic.AddInt64(&c.stepBroadcastQueueSize, -1)
			err := c.processStepBroadcast(msg.Position)
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
			// Update heartbeat for existing registration
			node.LastHeartbeat = time.Now()
			log.Printf("Re-registered existing node %s at position %d", req.PodID, pos)
			return RegisterResponse{Position: pos}
		}
	}
	
	// Find first available position
	position := -1
	for i := 0; i < 100; i++ {
		if _, exists := c.nodes[i]; !exists {
			position = i
			break
		}
	}
	
	if position == -1 {
		return RegisterResponse{Position: -1} // Will handle error in handler
	}
	
	// Convert position to row/col (10x10 grid layout)
	row := position / 10
	col := position % 10
	
	now := time.Now()
	c.nodes[position] = &NodeInfo{
		PodID:         req.PodID,
		DisplayName:   req.DisplayName, // Store display name if provided
		Position:      Position{Row: row, Col: col},
		Endpoint:      req.Endpoint,
		RegisteredAt:  now,
		LastHeartbeat: now,
	}
	
	log.Printf("Registered node %s at position %d", req.PodID, position)
	return RegisterResponse{Position: position}
}

// Process state update
func (c *Controller) processStateUpdate(position int, req StateUpdateRequest) error {
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
						nRow = 6 // Get neighbor's south edge
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
						neighborPos = row*10 + (col-1)
						nRow = haloRow - 1 // Map halo row 1-7 to grid row 0-6
						nCol = 6 // Get neighbor's east edge
					}
				} else if haloCol == 8 && haloRow >= 1 && haloRow <= 7 {
					// East edge - neighbor is position + 1
					if col < 9 {
						neighborPos = row*10 + (col+1)
						nRow = haloRow - 1
						nCol = 0 // Get neighbor's west edge
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
			"timestamp":    time.Now().Unix(),
			"regionId":     c.regionID,
			"generation":   atomic.LoadInt64(&c.currentGeneration),
			"nodes":        len(c.nodes),
			"activeGrids":  len(c.gridStates),
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

	log.Printf("Click at (%d,%d) targeting grid at position %d (engine %s at %s)",
		req.GlobalX, req.GlobalY, position, node.PodID, node.Endpoint)

	// Send randomize request to the target engine asynchronously
	go c.sendRandomizeToEngine(position)

	return nil
}

// sendRandomizeToEngine sends a POST /randomize to a specific engine
func (c *Controller) sendRandomizeToEngine(position int) {
	node, exists := c.nodes[position]
	if !exists {
		return
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(node.Endpoint+"/randomize", "application/json", nil)
	if err != nil {
		log.Printf("Failed to send randomize to engine %d: %v", position, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Engine %d rejected randomize: %d", position, resp.StatusCode)
	}
}

// processRandomizeAll sends a POST /randomize to every registered engine.
// Runs on the message processor goroutine; HTTP fan-out is dispatched in
// goroutines so the processor isn't blocked.
func (c *Controller) processRandomizeAll() int {
	client := &http.Client{Timeout: 2 * time.Second}

	var wg sync.WaitGroup
	var successCount int64

	for position := range c.nodes {
		wg.Add(1)
		go func(pos int, endpoint string) {
			defer wg.Done()
			resp, err := client.Post(endpoint+"/randomize", "application/json", nil)
			if err != nil {
				log.Printf("Failed to randomize engine %d: %v", pos, err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				atomic.AddInt64(&successCount, 1)
			}
		}(position, c.nodes[position].Endpoint)
	}

	wg.Wait()
	return int(successCount)
}

// broadcastStepToAllEngines sends step signal to all registered engines
func (c *Controller) broadcastStepToAllEngines() {
	// Advance generation first
	atomic.AddInt64(&c.currentGeneration, 1)
	newGeneration := atomic.LoadInt64(&c.currentGeneration)
	
	log.Printf("Broadcasting step signal for generation %d to %d engines", newGeneration, len(c.nodes))
	
	// Track which engines didn't participate in the last step
	c.updateMissedSteps()
	
	// Send step signal to all engines via HTTP
	for position := range c.nodes {
		go c.sendStepSignalToEngine(position)
	}
	
	// Clean up stale engines before next step
	c.cleanupStaleEngines()
	
	// Broadcast updated state to WebSocket clients
	c.broadcastToWebSocketClients()
	
	// Reset readiness tracking for next generation
	c.readyEngines = make(map[int]bool)
	c.stepInProgress = false
}

// updateMissedSteps increments missed step count for engines that didn't report ready
func (c *Controller) updateMissedSteps() {
	for position, node := range c.nodes {
		if c.readyEngines[position] {
			// Engine was ready, reset missed steps
			node.MissedSteps = 0
		} else {
			// Engine missed this step
			node.MissedSteps++
		}
	}
}

// cleanupStaleEngines removes engines that have missed too many steps
func (c *Controller) cleanupStaleEngines() {
	for position, node := range c.nodes {
		if node.MissedSteps >= 3 {
			log.Printf("Removing stale engine %s at position %d (missed %d steps)", 
				node.PodID, position, node.MissedSteps)
			delete(c.nodes, position)
			delete(c.gridStates, position)
		}
	}
}

// sendStepSignalToEngine sends step signal to a specific engine
func (c *Controller) sendStepSignalToEngine(position int) {
	node, exists := c.nodes[position]
	if !exists {
		return
	}
	
	// Create HTTP client for step signal
	client := &http.Client{Timeout: 2 * time.Second}
	
	// Send step signal to engine
	url := fmt.Sprintf("%s/step", node.Endpoint)
	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		log.Printf("Failed to send step signal to engine %d: %v", position, err)
		return
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		log.Printf("Engine %d rejected step signal: %d", position, resp.StatusCode)
	}
}

// processStepBroadcast handles step broadcast requests (if needed)
func (c *Controller) processStepBroadcast(position int) error {
	// This can be used for engine-initiated step requests if needed
	return nil
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

// barrierCoordinator manages the barrier synchronization for distributed stepping.
// It polls engine readiness at short intervals and broadcasts a step signal when
// either all engines report ready (fast path) or the barrier timeout elapses
// (slow path). Engines that don't report ready before the timeout are counted as
// missing the step and may be cleaned up by cleanupStaleEngines.
func (c *Controller) barrierCoordinator() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	lastStep := time.Now()

	for range ticker.C {
		if c.stepInProgress || len(c.nodes) == 0 || c.steppingPaused {
			continue
		}

		readyCount := len(c.readyEngines)
		totalEngines := len(c.nodes)
		elapsed := time.Since(lastStep)

		if readyCount >= totalEngines || elapsed >= c.barrierTimeout {
			log.Printf("Barrier sync: %d/%d engines ready (%v elapsed), broadcasting step for generation %d",
				readyCount, totalEngines, elapsed.Round(time.Millisecond), c.currentGeneration)

			c.stepInProgress = true
			lastStep = time.Now()
			go c.broadcastStepToAllEngines()
		}
	}
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

// POST /register
func (c *Controller) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	responseChan := make(chan RegisterResponse, 1)
	msg := &RegisterMessage{
		Request:  req,
		Response: responseChan,
	}
	
	// Try to queue the message
	select {
	case c.registerChan <- msg:
		atomic.AddInt64(&c.registerQueueSize, 1)
		// Wait for response
		response := <-responseChan
		if response.Position == -1 {
			http.Error(w, "Grid full", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	default:
		// Queue full
		http.Error(w, "Controller overloaded", http.StatusServiceUnavailable)
	}
}

// POST /state/{position}
func (c *Controller) handleStateUpdate(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	position, err := strconv.Atoi(vars["position"])
	if err != nil {
		http.Error(w, "Invalid position", http.StatusBadRequest)
		return
	}

	var req StateUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	responseChan := make(chan error, 1)
	msg := &StateUpdateMessage{
		Position: position,
		Request:  req,
		Response: responseChan,
	}
	
	// Try to queue the message
	select {
	case c.stateUpdateChan <- msg:
		atomic.AddInt64(&c.stateUpdateQueueSize, 1)
		// Wait for response
		if err := <-responseChan; err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		// Queue full
		http.Error(w, "Controller overloaded", http.StatusServiceUnavailable)
	}
}

// GET /halo/{position}
func (c *Controller) handleHaloRequest(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	position, err := strconv.Atoi(vars["position"])
	if err != nil {
		http.Error(w, "Invalid position", http.StatusBadRequest)
		return
	}

	responseChan := make(chan HaloResponse, 1)
	msg := &HaloReadMessage{
		Position: position,
		Response: responseChan,
	}
	
	// Try to queue the message
	select {
	case c.haloReadChan <- msg:
		atomic.AddInt64(&c.haloReadQueueSize, 1)
		// Wait for response
		response := <-responseChan
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	default:
		// Queue full
		http.Error(w, "Controller overloaded", http.StatusServiceUnavailable)
	}
}

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
		"status": "paused",
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
		"status": "running",
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
	
	if c.stepInProgress {
		http.Error(w, "Step already in progress", http.StatusConflict)
		return
	}
	
	log.Printf("DEBUG: Manual step requested at generation %d", atomic.LoadInt64(&c.currentGeneration))
	c.stepInProgress = true
	go c.broadcastStepToAllEngines()
	
	response := map[string]interface{}{
		"status": "step_triggered",
		"generation": atomic.LoadInt64(&c.currentGeneration) + 1,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
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
	r.HandleFunc("/register", controller.handleRegister).Methods("POST")
	r.HandleFunc("/state/{position}", controller.handleStateUpdate).Methods("POST")
	r.HandleFunc("/halo/{position}", controller.handleHaloRequest).Methods("GET")
	r.HandleFunc("/generation", controller.handleGeneration).Methods("GET")
	r.HandleFunc("/aggregated-state", controller.handleAggregatedState).Methods("GET")
	r.HandleFunc("/topology", controller.handleTopology).Methods("GET")
	r.HandleFunc("/api/click", controller.handleClick).Methods("POST")
	r.HandleFunc("/api/randomize", controller.handleRandomize).Methods("POST")
	r.HandleFunc("/health", controller.handleHealth).Methods("GET")
	
	// Legacy API mapping
	r.HandleFunc("/api/grid", controller.handleApiGrid).Methods("GET")
	
	// Metrics endpoint for web interface
	r.HandleFunc("/metrics", controller.handleMetrics).Methods("GET")
	
	// WebSocket endpoint for real-time updates
	r.HandleFunc("/ws", controller.handleWebSocket)
	
	// Debug endpoints
	r.HandleFunc("/debug/pause", controller.handleDebugPause).Methods("POST")
	r.HandleFunc("/debug/unpause", controller.handleDebugUnpause).Methods("POST")
	r.HandleFunc("/debug/nextstep", controller.handleDebugNextStep).Methods("POST")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	log.Printf("Game of Life Controller with Channels starting on port %s (Region: %s)", port, controller.regionID)
	log.Printf("Queue sizes: register=%d, stateUpdate=%d, haloRead=%d, webRead=%d",
		cap(controller.registerChan), cap(controller.stateUpdateChan), 
		cap(controller.haloReadChan), cap(controller.webReadChan))
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), r))
}