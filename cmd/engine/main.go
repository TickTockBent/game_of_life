package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/ticktockbent/game_of_life/pkg/gameoflife"
	"github.com/ticktockbent/game_of_life/pkg/grid"
)

type Engine struct {
	grid           *gameoflife.Grid
	distributedGrid *grid.DistributedGrid
	nodeID         string
	running        bool
	ticker         *time.Ticker
	controllerURL  string
	selfEndpoint   string
	position       int
	registered     bool
	neighbors      map[string]string
	crosstalkEnabled bool
	lastEmptyCount int // Track boring threshold resets
	
	// WebSocket edge communication
	neighborConns  map[string]*websocket.Conn
	connMutex      sync.RWMutex
	upgrader       websocket.Upgrader
}

type GridStateResponse struct {
	NodeID     string                      `json:"nodeId"`
	Generation int                         `json:"generation"`
	Grid       [][gameoflife.GridSize]bool `json:"grid"`
	Edges      map[string][]bool           `json:"edges"`
}

type CellUpdateRequest struct {
	X     int  `json:"x"`
	Y     int  `json:"y"`
	Alive bool `json:"alive"`
}

type EdgeUpdate struct {
	Direction string `json:"direction"`
	EdgeData  []bool `json:"edgeData"`
	NodeID    string `json:"nodeId"`
}

func NewEngine() *Engine {
	nodeID := os.Getenv("NODE_NAME")
	if nodeID == "" {
		nodeID = "standalone"
	}
	
	controllerURL := os.Getenv("CONTROLLER_URL")
	selfEndpoint := os.Getenv("SELF_ENDPOINT")
	
	engine := &Engine{
		grid:          gameoflife.NewGrid(),
		nodeID:        nodeID,
		controllerURL: controllerURL,
		selfEndpoint:  selfEndpoint,
		neighborConns: make(map[string]*websocket.Conn),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow connections from any origin for distributed setup
			},
		},
	}
	
	// Initialize distributed grid if controller URL is provided
	if controllerURL != "" && selfEndpoint != "" {
		engine.distributedGrid = grid.NewDistributedGrid(controllerURL, selfEndpoint)
	}
	
	// Start periodic health checking if configured for distributed mode
	if controllerURL != "" && selfEndpoint != "" {
		go engine.healthCheckLoop()
	}
	
	return engine
}

// healthCheckLoop periodically verifies registration with controller
func (e *Engine) healthCheckLoop() {
	ticker := time.NewTicker(5 * time.Second) // Check every 5 seconds (less aggressive than controller)
	defer ticker.Stop()
	
	for range ticker.C {
		e.verifyRegistration()
	}
}

// verifyRegistration checks if we're still registered and re-registers if needed
func (e *Engine) verifyRegistration() {
	if e.controllerURL == "" || e.selfEndpoint == "" {
		return
	}
	
	// Check if controller is reachable and if we're still registered
	if !e.isControllerHealthy() || !e.isRegisteredWithController() {
		log.Printf("Lost connection to controller, attempting re-registration...")
		e.registered = false
		e.attemptRegistration()
	}
}

// isControllerHealthy checks if controller responds to health check
func (e *Engine) isControllerHealthy() bool {
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(e.controllerURL + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// isRegisteredWithController verifies our registration status
func (e *Engine) isRegisteredWithController() bool {
	if !e.registered {
		return false
	}
	
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(fmt.Sprintf("%s/node/%d", e.controllerURL, e.position))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	
	return resp.StatusCode == 200
}

// attemptRegistration tries to register with the controller
func (e *Engine) attemptRegistration() {
	if e.distributedGrid == nil {
		e.distributedGrid = grid.NewDistributedGrid(e.controllerURL, e.selfEndpoint)
	}
	
	if err := e.distributedGrid.Register(e.nodeID); err != nil {
		log.Printf("Failed to re-register with controller: %v", err)
		e.registered = false
		// Health endpoint will now return unhealthy status, triggering k8s restart
		log.Printf("Marking as unregistered - health checks will fail to trigger pod restart")
	} else {
		e.position = e.distributedGrid.GetPosition()
		e.registered = true
		log.Printf("Successfully re-registered with controller at position %d", e.position)
		
		// Discover neighbors and enable crosstalk
		e.discoverNeighbors()
		
		// Start barrier sync after re-registration
		e.startBarrierSyncLoop()
	}
}

// startBarrierSyncLoop starts the barrier-synchronized Game of Life simulation
func (e *Engine) startBarrierSyncLoop() {
	if e.running {
		return // Already running
	}
	
	e.running = true
	
	go func() {
		for e.running {
			// 1. Poll neighbors for current edge state
			e.pollNeighborEdges()
			
			// 2. Compute next generation locally (no state change yet)
			e.computeNextGeneration()
			
			// 3. Signal ready to controller
			e.signalReadyToController()
			
			// 4. Wait for controller's step command
			e.waitForStepCommand()
			
			// 5. Atomically commit the new state
			e.commitNextGeneration()
		}
	}()
	
	log.Printf("Started barrier-synchronized simulation loop")
}

// pollNeighborEdges fetches current edge state from all neighbors
func (e *Engine) pollNeighborEdges() {
	if !e.crosstalkEnabled || len(e.neighbors) == 0 {
		return
	}
	
	for direction, endpoint := range e.neighbors {
		// Get current edge state from neighbor
		client := &http.Client{Timeout: 100 * time.Millisecond}
		
		// Determine which edge to request based on our position relative to neighbor
		var requestDirection string
		switch direction {
		case "north":
			requestDirection = "south" // Their south edge becomes our north halo
		case "south":
			requestDirection = "north" // Their north edge becomes our south halo
		case "east":
			requestDirection = "west"  // Their west edge becomes our east halo
		case "west":
			requestDirection = "east"  // Their east edge becomes our west halo
		default:
			continue
		}
		
		resp, err := client.Get(endpoint + "/edges/" + requestDirection)
		if err != nil {
			// Neighbor not available, use empty edge data
			continue
		}
		defer resp.Body.Close()
		
		if resp.StatusCode == 200 {
			var edgeData []bool
			if err := json.NewDecoder(resp.Body).Decode(&edgeData); err == nil {
				e.grid.UpdateHaloRegion(direction, edgeData)
			}
		}
	}
}

// computeNextGeneration calculates the next state but doesn't commit it yet
func (e *Engine) computeNextGeneration() {
	// Track empty generations for boring threshold detection
	emptyBefore := e.grid.GetEmptyGenerations()
	
	// Compute next generation (this modifies nextGen field, not current cells)
	e.grid.NextGeneration()
	
	// Check if boring threshold reset occurred
	emptyAfter := e.grid.GetEmptyGenerations()
	if emptyBefore >= 99 && emptyAfter == 0 {
		log.Printf("🎲 Boring threshold reached! Auto-randomized grid after %d empty generations", emptyBefore)
	}
}

// signalReadyToController tells controller this engine is ready for next step
func (e *Engine) signalReadyToController() {
	if e.controllerURL == "" || !e.registered {
		return
	}
	
	// Signal ready to controller (this endpoint needs to be added to controller)
	client := &http.Client{Timeout: 1 * time.Second}
	readyData := map[string]interface{}{
		"nodeId":   e.nodeID,
		"position": e.position,
		"generation": e.grid.GetGeneration(),
	}
	
	data, _ := json.Marshal(readyData)
	resp, err := client.Post(e.controllerURL+"/ready", "application/json", bytes.NewBuffer(data))
	if err != nil {
		log.Printf("Failed to signal ready to controller: %v", err)
		return
	}
	defer resp.Body.Close()
}

// waitForStepCommand waits for controller to broadcast step advance
func (e *Engine) waitForStepCommand() {
	if e.controllerURL == "" || !e.registered {
		// In standalone mode, just advance after a delay
		time.Sleep(200 * time.Millisecond)
		return
	}
	
	// Poll controller for step command (this could be optimized with WebSockets later)
	client := &http.Client{Timeout: 5 * time.Second}
	for {
		resp, err := client.Get(fmt.Sprintf("%s/step-ready/%d", e.controllerURL, e.position))
		if err != nil {
			log.Printf("Failed to check step ready: %v", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		defer resp.Body.Close()
		
		if resp.StatusCode == 200 {
			// Controller says we can advance
			break
		}
		
		// Not ready yet, wait a bit and check again
		time.Sleep(50 * time.Millisecond)
	}
}

// commitNextGeneration atomically swaps in the computed next state
func (e *Engine) commitNextGeneration() {
	// This is where the actual state change happens
	// Since we removed locks, this is just a simple assignment
	e.grid.Cells = e.grid.NextGen
	e.grid.Generation++
}

func (e *Engine) handleGetState(w http.ResponseWriter, r *http.Request) {
	state, gen := e.grid.GetState()
	edges := e.grid.GetEdgeCells()
	
	// Validate that we have a complete 7x7 grid to catch any partial reads
	if len(state) != gameoflife.GridSize {
		log.Printf("Invalid state: expected %d rows, got %d", gameoflife.GridSize, len(state))
		// Return empty grid rather than potentially corrupted data
		state = make([][gameoflife.GridSize]bool, gameoflife.GridSize)
		gen = 0
	} else {
		for i, row := range state {
			if len(row) != gameoflife.GridSize {
				log.Printf("Invalid state: row %d expected %d columns, got %d", i, gameoflife.GridSize, len(row))
				// Return empty grid rather than potentially corrupted data
				state = make([][gameoflife.GridSize]bool, gameoflife.GridSize)
				gen = 0
				break
			}
		}
	}
	
	response := GridStateResponse{
		NodeID:     e.nodeID,
		Generation: gen,
		Grid:       state,
		Edges:      edges,
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (e *Engine) handleUpdateCell(w http.ResponseWriter, r *http.Request) {
	var req CellUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	
	log.Printf("Setting cell (%d, %d) to %t", req.X, req.Y, req.Alive)
	e.grid.SetCell(req.X, req.Y, req.Alive)
	w.WriteHeader(http.StatusOK)
}

func (e *Engine) handleStart(w http.ResponseWriter, r *http.Request) {
	if e.running {
		http.Error(w, "Already running", http.StatusBadRequest)
		return
	}
	
	e.running = true
	e.ticker = time.NewTicker(100 * time.Millisecond)
	
	go func() {
		for range e.ticker.C {
			if e.running {
				// Track empty generations for boring threshold detection
				emptyBefore := e.grid.GetEmptyGenerations()
				
				e.grid.NextGeneration()
				
				// Check if boring threshold reset occurred
				emptyAfter := e.grid.GetEmptyGenerations()
				if emptyBefore >= 99 && emptyAfter == 0 {
					log.Printf("🎲 Boring threshold reached! Auto-randomized grid after %d empty generations", emptyBefore)
				}
			}
		}
	}()
	
	w.WriteHeader(http.StatusOK)
}

func (e *Engine) handleStop(w http.ResponseWriter, r *http.Request) {
	if !e.running {
		http.Error(w, "Not running", http.StatusBadRequest)
		return
	}
	
	e.running = false
	if e.ticker != nil {
		e.ticker.Stop()
	}
	
	w.WriteHeader(http.StatusOK)
}

func (e *Engine) handleStep(w http.ResponseWriter, r *http.Request) {
	if e.running {
		http.Error(w, "Cannot step while running", http.StatusBadRequest)
		return
	}
	
	e.grid.NextGeneration()
	w.WriteHeader(http.StatusOK)
}

func (e *Engine) handleRandomize(w http.ResponseWriter, r *http.Request) {
	log.Printf("Randomizing grid with 30%% probability")
	e.grid.RandomSeed(0.3)
	w.WriteHeader(http.StatusOK)
}

func (e *Engine) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Simple registration-based health check - no locks required
	status := "healthy"
	if e.controllerURL != "" {
		// In distributed mode, health depends on registration status
		if !e.registered {
			status = "unhealthy"
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}
	
	health := map[string]interface{}{
		"status": status,
		"nodeId": e.nodeID,
		"registered": e.registered,
	}
	
	if e.distributedGrid != nil && e.registered {
		health["position"] = e.distributedGrid.GetPosition()
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

func (e *Engine) handleGetEdge(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	direction := vars["direction"]
	
	edges := e.grid.GetEdgeCells()
	edgeData, exists := edges[direction]
	
	if !exists {
		http.Error(w, "Invalid direction", http.StatusBadRequest)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(edgeData)
}

// discoverNeighbors queries the controller for neighbor endpoints
func (e *Engine) discoverNeighbors() {
	if e.controllerURL == "" || !e.registered {
		return
	}
	
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("%s/neighbors/%d", e.controllerURL, e.position))
	if err != nil {
		log.Printf("Failed to discover neighbors: %v", err)
		return
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		log.Printf("Failed to get neighbors, status: %d", resp.StatusCode)
		return
	}
	
	var neighbors map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&neighbors); err != nil {
		log.Printf("Failed to decode neighbors: %v", err)
		return
	}
	
	// Extract neighbor endpoints
	neighborEndpoints := make(map[string]string)
	for direction, nodeInfo := range neighbors {
		if nodeMap, ok := nodeInfo.(map[string]interface{}); ok {
			if endpoint, exists := nodeMap["endpoint"]; exists {
				if endpointStr, ok := endpoint.(string); ok {
					neighborEndpoints[direction] = endpointStr
				}
			}
		}
	}
	
	e.neighbors = neighborEndpoints
	e.crosstalkEnabled = len(neighborEndpoints) > 0
	
	// Configure the grid with neighbor endpoints
	e.grid.SetNeighbors(neighborEndpoints)
	
	// Establish WebSocket connections to neighbors
	go e.connectToNeighbors()
	
	log.Printf("Discovered %d neighbors for edge crosstalk: %v", len(neighborEndpoints), neighborEndpoints)
}

func main() {
	engine := NewEngine()
	
	engine.grid.RandomSeed(0.3)
	
	r := mux.NewRouter()
	r.HandleFunc("/state", engine.handleGetState).Methods("GET")
	r.HandleFunc("/cell", engine.handleUpdateCell).Methods("POST")
	r.HandleFunc("/start", engine.handleStart).Methods("POST")
	r.HandleFunc("/stop", engine.handleStop).Methods("POST")
	r.HandleFunc("/step", engine.handleStep).Methods("POST")
	r.HandleFunc("/randomize", engine.handleRandomize).Methods("POST")
	r.HandleFunc("/health", engine.handleHealth).Methods("GET")
	r.HandleFunc("/edges/{direction}", engine.handleGetEdge).Methods("GET")
	r.HandleFunc("/neighbors/refresh", engine.handleRefreshNeighbors).Methods("POST")
	r.HandleFunc("/ws/edges", engine.handleWebSocketEdges).Methods("GET")
	
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	
	// Register with controller if configured
	if engine.distributedGrid != nil {
		if err := engine.distributedGrid.Register(engine.nodeID); err != nil {
			log.Printf("Failed to register with controller: %v", err)
			engine.registered = false
			// Health endpoint will return unhealthy status, triggering k8s restart
			log.Printf("Initial registration failed - health checks will fail to trigger pod restart")
		} else {
			engine.position = engine.distributedGrid.GetPosition()
			engine.registered = true
			log.Printf("Registered with controller at position %d", engine.position)
			
			// Discover neighbors and enable crosstalk
			engine.discoverNeighbors()
			
			// Start barrier-synchronized simulation
			engine.startBarrierSyncLoop()
		}
	}
	
	// Periodic neighbor discovery to handle topology changes
	if engine.distributedGrid != nil {
		go engine.neighborDiscoveryLoop()
	}
	
	log.Printf("Game of Life Engine starting on port %s (Node: %s)\n", port, engine.nodeID)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), r))
}

// neighborDiscoveryLoop periodically refreshes neighbor information
func (e *Engine) neighborDiscoveryLoop() {
	ticker := time.NewTicker(10 * time.Second) // Refresh neighbors every 10 seconds
	defer ticker.Stop()
	
	for range ticker.C {
		if e.registered {
			e.discoverNeighbors()
		}
	}
}

// handleRefreshNeighbors manually triggers neighbor discovery
func (e *Engine) handleRefreshNeighbors(w http.ResponseWriter, r *http.Request) {
	e.discoverNeighbors()
	response := map[string]interface{}{
		"neighbors": e.neighbors,
		"crosstalkEnabled": e.crosstalkEnabled,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleWebSocketEdges handles WebSocket connections for edge updates
func (e *Engine) handleWebSocketEdges(w http.ResponseWriter, r *http.Request) {
	conn, err := e.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()
	
	log.Printf("WebSocket edge connection established from %s", r.RemoteAddr)
	
	// Handle incoming edge updates
	for {
		var update EdgeUpdate
		err := conn.ReadJSON(&update)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket edge connection error: %v", err)
			}
			break
		}
		
		// Apply the edge update to our grid's halo region
		e.grid.UpdateHaloRegion(update.Direction, update.EdgeData)
		log.Printf("Received edge update for %s from %s", update.Direction, update.NodeID)
	}
}

// broadcastEdgeUpdates sends our edge data to all neighbor WebSocket connections
func (e *Engine) broadcastEdgeUpdates() {
	if !e.crosstalkEnabled {
		return
	}
	
	edges := e.grid.GetEdgeCells()
	
	e.connMutex.RLock()
	defer e.connMutex.RUnlock()
	
	// Send appropriate edge data to each neighbor
	for direction, conn := range e.neighborConns {
		if conn == nil {
			continue
		}
		
		// Determine which edge to send based on neighbor direction
		var edgeData []bool
		var edgeDirection string
		
		switch direction {
		case "north":
			edgeData = edges["north"]
			edgeDirection = "south" // Our north edge becomes their south halo
		case "south":
			edgeData = edges["south"]
			edgeDirection = "north" // Our south edge becomes their north halo
		case "east":
			edgeData = edges["east"]
			edgeDirection = "west" // Our east edge becomes their west halo
		case "west":
			edgeData = edges["west"]
			edgeDirection = "east" // Our west edge becomes their east halo
		default:
			continue
		}
		
		update := EdgeUpdate{
			Direction: edgeDirection,
			EdgeData:  edgeData,
			NodeID:    e.nodeID,
		}
		
		// Send update asynchronously to avoid blocking
		go func(conn *websocket.Conn, direction string, update EdgeUpdate) {
			err := conn.WriteJSON(update)
			if err != nil {
				log.Printf("Failed to send edge update to %s neighbor: %v", direction, err)
				// Mark connection as failed - it will be reconnected later
				e.connMutex.Lock()
				e.neighborConns[direction] = nil
				e.connMutex.Unlock()
			}
		}(conn, direction, update)
	}
}

// connectToNeighbors establishes WebSocket connections to all discovered neighbors
func (e *Engine) connectToNeighbors() {
	if !e.crosstalkEnabled || len(e.neighbors) == 0 {
		return
	}
	
	e.connMutex.Lock()
	defer e.connMutex.Unlock()
	
	for direction, endpoint := range e.neighbors {
		// Skip if already connected
		if e.neighborConns[direction] != nil {
			continue
		}
		
		// Convert HTTP endpoint to WebSocket URL
		u, err := url.Parse(endpoint)
		if err != nil {
			log.Printf("Failed to parse neighbor endpoint %s: %v", endpoint, err)
			continue
		}
		
		wsURL := fmt.Sprintf("ws://%s/ws/edges", u.Host)
		
		go func(direction, wsURL string) {
			for {
				conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
				if err != nil {
					log.Printf("Failed to connect to %s neighbor at %s: %v", direction, wsURL, err)
					time.Sleep(5 * time.Second) // Retry after 5 seconds
					continue
				}
				
				log.Printf("Connected to %s neighbor via WebSocket: %s", direction, wsURL)
				
				e.connMutex.Lock()
				e.neighborConns[direction] = conn
				e.connMutex.Unlock()
				
				// Monitor connection and reconnect if it fails
				for {
					_, _, err := conn.ReadMessage()
					if err != nil {
						log.Printf("WebSocket connection to %s lost: %v", direction, err)
						conn.Close()
						
						e.connMutex.Lock()
						e.neighborConns[direction] = nil
						e.connMutex.Unlock()
						
						break // Will retry connection in outer loop
					}
				}
				
				time.Sleep(2 * time.Second) // Brief pause before reconnection
			}
		}(direction, wsURL)
	}
}