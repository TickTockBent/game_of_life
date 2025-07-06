package main

import (
	"encoding/json"
	"fmt"
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
	
	controllerURL := os.Getenv("CONTROLLER_URL")
	selfEndpoint := os.Getenv("SELF_ENDPOINT")
	
	engine := &Engine{
		grid:          gameoflife.NewGrid(),
		nodeID:        nodeID,
		controllerURL: controllerURL,
		selfEndpoint:  selfEndpoint,
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
	} else {
		e.position = e.distributedGrid.GetPosition()
		e.registered = true
		log.Printf("Successfully re-registered with controller at position %d", e.position)
		
		// Discover neighbors and enable crosstalk
		e.discoverNeighbors()
		
		// Auto-start after re-registration
		e.startSimulation()
	}
}

// startSimulation starts the Game of Life simulation
func (e *Engine) startSimulation() {
	if e.running {
		return // Already running
	}
	
	e.running = true
	e.ticker = time.NewTicker(100 * time.Millisecond)
	
	go func() {
		for range e.ticker.C {
			if !e.running {
				break
			}
			
			// Track empty generations for boring threshold detection
			emptyBefore := e.grid.GetEmptyGenerations()
			
			e.grid.NextGeneration()
			
			// Check if boring threshold reset occurred (empty count went from high to 0)
			emptyAfter := e.grid.GetEmptyGenerations()
			if emptyBefore >= 99 && emptyAfter == 0 {
				log.Printf("🎲 Boring threshold reached! Auto-randomized grid after %d empty generations", emptyBefore)
			}
		}
	}()
	
	log.Printf("Auto-started simulation for continuous display")
}

func (e *Engine) handleGetState(w http.ResponseWriter, r *http.Request) {
	state, gen := e.grid.GetState()
	edges := e.grid.GetEdgeCells()
	
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
	health := map[string]interface{}{
		"status": "healthy",
		"nodeId": e.nodeID,
		"generation": e.grid.GetGeneration(),
	}
	
	if e.distributedGrid != nil {
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
	
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	
	// Register with controller if configured
	if engine.distributedGrid != nil {
		if err := engine.distributedGrid.Register(engine.nodeID); err != nil {
			log.Printf("Failed to register with controller: %v", err)
			engine.registered = false
			// Continue running in standalone mode
		} else {
			engine.position = engine.distributedGrid.GetPosition()
			engine.registered = true
			log.Printf("Registered with controller at position %d", engine.position)
			
			// Discover neighbors and enable crosstalk
			engine.discoverNeighbors()
			
			// Auto-start the simulation for continuous public display
			engine.startSimulation()
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