package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
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
	isReady        bool // Ready status for barrier sync
	httpClient     *http.Client // Shared HTTP client to prevent memory leaks
	stopChan       chan struct{} // Channel to signal goroutines to stop
	// Pre-allocated buffer for JSON edge data to avoid allocations
	edgeBuffer     []bool
	// Track if sync loop is running to prevent duplicates
	syncLoopRunning bool
	// Separate stop channels for each goroutine
	healthStopChan    chan struct{}
	syncStopChan      chan struct{}
	neighborStopChan  chan struct{}
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

func NewEngine() *Engine {
	nodeID := os.Getenv("NODE_NAME")
	if nodeID == "" {
		nodeID = "standalone"
	}
	
	// Always use external controller URL for global connectivity
	controllerURL := "https://gameoflife-api.ticktockbent.com"
	selfEndpoint := os.Getenv("SELF_ENDPOINT")
	
	// Create shared HTTP client with optimized settings for frequent polling
	httpClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			MaxIdleConns:        5,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	engine := &Engine{
		grid:          gameoflife.NewGrid(),
		nodeID:        nodeID,
		controllerURL: controllerURL,
		selfEndpoint:  selfEndpoint,
		httpClient:    httpClient,
		stopChan:      make(chan struct{}),
		// Pre-allocate edge buffer for JSON unmarshaling
		edgeBuffer:    make([]bool, gameoflife.GridSize),
		// Initialize separate stop channels
		healthStopChan:   make(chan struct{}),
		syncStopChan:     make(chan struct{}),
		neighborStopChan: make(chan struct{}),
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
	
	for {
		select {
		case <-ticker.C:
			e.verifyRegistration()
		case <-e.healthStopChan:
			log.Printf("Health check loop stopped")
			return
		}
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
	resp, err := e.httpClient.Get(e.controllerURL + "/health")
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
	
	resp, err := e.httpClient.Get(fmt.Sprintf("%s/node/%d", e.controllerURL, e.position))
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
		
		// Stop existing loops before starting new ones
		e.stopExistingLoops()
		
		// Discover neighbors and enable crosstalk
		e.discoverNeighbors()
		
		// Start barrier sync loop after re-registration
		e.startBarrierSyncLoop()
	}
}

// startBarrierSyncLoop starts barrier-synchronized Game of Life simulation
func (e *Engine) startBarrierSyncLoop() {
	// Prevent multiple sync loops
	if e.syncLoopRunning {
		log.Printf("Barrier sync loop already running, skipping start")
		return
	}
	
	e.running = true
	e.syncLoopRunning = true
	
	go func() {
		defer func() {
			log.Printf("Barrier sync loop stopped")
			e.syncLoopRunning = false
		}()
		
		ticker := time.NewTicker(100 * time.Millisecond) // Reduced frequency to save memory
		defer ticker.Stop()
		
		for e.running {
			select {
			case <-ticker.C:
				// 1. Poll neighbors for edge state (quick HTTP calls)
				e.pollNeighborEdges()
				
				// 2. Compute next generation but don't commit yet
				emptyBefore := e.grid.GetEmptyGenerations()
				e.grid.ComputeNextGeneration()
				
				// 3. Mark ready and wait for controller to send step command
				e.isReady = true
				
				// Wait for step command (controller will call our /step endpoint)
				for e.isReady && e.running {
					select {
					case <-time.After(50 * time.Millisecond):
						// Continue polling
					case <-e.syncStopChan:
						return
					}
				}
				
				// Check boring threshold after step
				emptyAfter := e.grid.GetEmptyGenerations()
				if emptyBefore >= 99 && emptyAfter == 0 {
					log.Printf("🎲 Boring threshold reached! Auto-randomized grid after %d empty generations", emptyBefore)
				}
			case <-e.syncStopChan:
				return
			}
		}
	}()
	
	log.Printf("Started polling-based barrier sync loop")
}

// pollNeighborEdges fetches current edge state from all neighbors
func (e *Engine) pollNeighborEdges() {
	if !e.crosstalkEnabled || len(e.neighbors) == 0 {
		return
	}
	
	for direction, endpoint := range e.neighbors {
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
		
		resp, err := e.httpClient.Get(endpoint + "/edges/" + requestDirection)
		if err != nil {
			// Neighbor not available, use empty edge data
			continue
		}
		
		// Process response and close body immediately
		if resp.StatusCode == 200 {
			// Reuse pre-allocated buffer instead of creating new slice
			if err := json.NewDecoder(resp.Body).Decode(&e.edgeBuffer); err == nil {
				e.grid.UpdateHaloRegion(direction, e.edgeBuffer)
			}
		}
		// Drain and close body to ensure connection reuse
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close() // Close immediately, not deferred
	}
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
	
	e.startBarrierSyncLoop()
	w.WriteHeader(http.StatusOK)
}

func (e *Engine) handleStop(w http.ResponseWriter, r *http.Request) {
	if !e.running {
		http.Error(w, "Not running", http.StatusBadRequest)
		return
	}
	
	e.Stop()
	w.WriteHeader(http.StatusOK)
}

// Stop gracefully shuts down the engine and cleans up goroutines
func (e *Engine) Stop() {
	e.running = false
	if e.ticker != nil {
		e.ticker.Stop()
	}
	
	// Signal all goroutines to stop
	close(e.stopChan)
	log.Printf("Engine stopped and cleanup completed")
}

// Old handleStep method removed - using new barrier sync version

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

// handleReady returns the current ready status for barrier sync
func (e *Engine) handleReady(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ready": e.isReady && e.registered,
		"nodeId": e.nodeID,
		"position": e.position,
	})
}

// handleStep receives step command from controller
func (e *Engine) handleStep(w http.ResponseWriter, r *http.Request) {
	if !e.registered || !e.isReady {
		http.Error(w, "Not ready for step", http.StatusBadRequest)
		return
	}
	
	// Commit the computed generation and mark not ready
	e.grid.CommitNextGeneration()
	e.isReady = false
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"stepped": true,
		"generation": e.grid.Generation,
	})
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
	
	resp, err := e.httpClient.Get(fmt.Sprintf("%s/neighbors/%d", e.controllerURL, e.position))
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
	
	log.Printf("Discovered %d neighbors for edge crosstalk: %v", len(neighborEndpoints), neighborEndpoints)
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

func main() {
	engine := NewEngine()
	
	engine.grid.RandomSeed(0.3)
	
	r := mux.NewRouter()
	r.HandleFunc("/state", engine.handleGetState).Methods("GET")
	r.HandleFunc("/cell", engine.handleUpdateCell).Methods("POST")
	r.HandleFunc("/start", engine.handleStart).Methods("POST")
	r.HandleFunc("/stop", engine.handleStop).Methods("POST")
	r.HandleFunc("/randomize", engine.handleRandomize).Methods("POST")
	r.HandleFunc("/health", engine.handleHealth).Methods("GET")
	r.HandleFunc("/ready", engine.handleReady).Methods("GET")
	r.HandleFunc("/step", engine.handleStep).Methods("POST")
	r.HandleFunc("/edges/{direction}", engine.handleGetEdge).Methods("GET")
	r.HandleFunc("/neighbors/refresh", engine.handleRefreshNeighbors).Methods("POST")
	
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
	
	for {
		select {
		case <-ticker.C:
			if e.registered {
				e.discoverNeighbors()
			}
		case <-e.stopChan:
			log.Printf("Neighbor discovery loop stopped")
			return
		}
	}
}

// stopExistingLoops stops all running goroutines before re-registration
func (e *Engine) stopExistingLoops() {
	// Stop sync loop if running
	if e.syncLoopRunning {
		log.Printf("Stopping existing barrier sync loop before re-registration")
		if e.syncStopChan != nil {
			select {
			case <-e.syncStopChan:
				// Already closed
			default:
				close(e.syncStopChan)
			}
		}
		// Wait a bit for the loop to stop
		time.Sleep(200 * time.Millisecond)
		// Create a new channel for the next loop
		e.syncStopChan = make(chan struct{})
		e.syncLoopRunning = false
		e.running = false // Reset running state so new loop can start
	}
}

// Old barrier sync methods removed - now using polling approach