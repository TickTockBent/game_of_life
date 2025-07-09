package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

// Simple controller for star topology - just aggregates state and serves halo data
type Controller struct {
	// Node registry
	nodes      map[int]*NodeInfo
	nodesMu    sync.RWMutex
	
	// Grid state storage
	gridStates map[int]*GridState
	stateMu    sync.RWMutex
	
	// Generation counter for lazy sync
	currentGeneration int64
	generationMu      sync.RWMutex
	
	// Simple metrics
	regionID   string
}

type NodeInfo struct {
	PodID        string    `json:"podId"`
	Position     Position  `json:"position"`
	Endpoint     string    `json:"endpoint"`
	RegisteredAt time.Time `json:"registeredAt"`
	LastHeartbeat time.Time `json:"lastHeartbeat"`
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

// Request/Response types
type RegisterRequest struct {
	PodID    string `json:"podId"`
	Endpoint string `json:"endpoint"`
}

type RegisterResponse struct {
	Position int `json:"position"`
}

type StateUpdateRequest struct {
	Grid       [][]bool `json:"grid"`
	Generation int      `json:"generation"`
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
		nodes:             make(map[int]*NodeInfo),
		gridStates:        make(map[int]*GridState),
		currentGeneration: 0,
		regionID:          regionID,
	}
	
	// Start generation advancement timer
	go c.generationAdvancer()
	
	// Start health check timer
	go c.healthChecker()
	
	return c
}

// generationAdvancer increments the global generation counter every 200ms
func (c *Controller) generationAdvancer() {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	
	for range ticker.C {
		c.generationMu.Lock()
		c.currentGeneration++
		c.generationMu.Unlock()
	}
}

// healthChecker removes stale nodes that haven't sent heartbeats
func (c *Controller) healthChecker() {
	ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds
	defer ticker.Stop()
	
	for range ticker.C {
		now := time.Now()
		staleThreshold := 2 * time.Minute // Consider stale after 2 minutes
		
		c.nodesMu.Lock()
		stalePods := []int{}
		for pos, node := range c.nodes {
			if now.Sub(node.LastHeartbeat) > staleThreshold {
				stalePods = append(stalePods, pos)
				log.Printf("Removing stale node %s at position %d (last heartbeat: %v ago)",
					node.PodID, pos, now.Sub(node.LastHeartbeat))
			}
		}
		
		// Remove stale nodes and clean up their resources
		for _, pos := range stalePods {
			delete(c.nodes, pos)
		}
		c.nodesMu.Unlock()
		
		// Clean up grid states separately to avoid nested locks
		if len(stalePods) > 0 {
			c.stateMu.Lock()
			for _, pos := range stalePods {
				delete(c.gridStates, pos)
			}
			c.stateMu.Unlock()
		}
		
		if len(stalePods) > 0 {
			log.Printf("Health check removed %d stale nodes", len(stalePods))
		}
	}
}

// GET /generation - Return current generation for engines to sync against
func (c *Controller) handleGeneration(w http.ResponseWriter, r *http.Request) {
	c.generationMu.RLock()
	gen := c.currentGeneration
	c.generationMu.RUnlock()
	
	response := map[string]interface{}{
		"generation": gen,
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// POST /register - Assign position to new engine
func (c *Controller) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	c.nodesMu.Lock()
	
	// Check if this pod is already registered
	for pos, node := range c.nodes {
		if node.PodID == req.PodID {
			// Update heartbeat for existing registration
			node.LastHeartbeat = time.Now()
			c.nodesMu.Unlock()
			
			response := RegisterResponse{Position: pos}
			log.Printf("Re-registered existing node %s at position %d", req.PodID, pos)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
	}
	
	// Find first available position (reuse positions from removed nodes)
	position := -1
	for i := 0; i < 100; i++ {
		if _, exists := c.nodes[i]; !exists {
			position = i
			break
		}
	}
	
	if position == -1 {
		c.nodesMu.Unlock()
		http.Error(w, "Grid full", http.StatusServiceUnavailable)
		return
	}
	
	// Convert position to row/col (10x10 grid layout)
	row := position / 10
	col := position % 10
	
	now := time.Now()
	c.nodes[position] = &NodeInfo{
		PodID:        req.PodID,
		Position:     Position{Row: row, Col: col},
		Endpoint:     req.Endpoint,
		RegisteredAt: now,
		LastHeartbeat: now,
	}
	c.nodesMu.Unlock()

	response := RegisterResponse{Position: position}
	log.Printf("Registered node %s at position %d", req.PodID, position)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// POST /state/{position} - Store grid state from engine
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

	// Update grid state
	c.stateMu.Lock()
	c.gridStates[position] = &GridState{
		Grid:       req.Grid,
		Generation: req.Generation,
		UpdatedAt:  time.Now(),
	}
	c.stateMu.Unlock()
	
	// Update heartbeat for this node
	c.nodesMu.Lock()
	if node, exists := c.nodes[position]; exists {
		node.LastHeartbeat = time.Now()
	}
	c.nodesMu.Unlock()

	w.WriteHeader(http.StatusOK)
}

// GET /halo/{position} - Return 9x9 region around position for edge computation
func (c *Controller) handleHaloRequest(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	position, err := strconv.Atoi(vars["position"])
	if err != nil {
		http.Error(w, "Invalid position", http.StatusBadRequest)
		return
	}

	// Calculate grid coordinates (10x10 layout)
	row := position / 10
	col := position % 10

	var halo [9][9]bool

	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

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
				// Edge cells come from neighbors
				neighborRow := row + (haloRow - 4)
				neighborCol := col + (haloCol - 4)
				
				if neighborRow >= 0 && neighborRow < 10 && neighborCol >= 0 && neighborCol < 10 {
					neighborPos := neighborRow*10 + neighborCol
					if state, exists := c.gridStates[neighborPos]; exists {
						// Map halo edge to neighbor's opposite edge
						nRow := (haloRow + 3) % 7
						nCol := (haloCol + 3) % 7
						if nRow < len(state.Grid) && nCol < len(state.Grid[nRow]) {
							halo[haloRow][haloCol] = state.Grid[nRow][nCol]
						}
					}
				}
			}
		}
	}

	response := HaloResponse{HaloCells: halo}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GET /aggregated-state - Return all grid states for web interface
func (c *Controller) handleAggregatedState(w http.ResponseWriter, r *http.Request) {
	c.nodesMu.RLock()
	nodes := make(map[int]*NodeInfo)
	for k, v := range c.nodes {
		nodes[k] = v
	}
	c.nodesMu.RUnlock()

	c.stateMu.RLock()
	grids := make(map[string]*GridState)
	for position, state := range c.gridStates {
		grids[strconv.Itoa(position)] = state
	}
	c.stateMu.RUnlock()

	response := &AggregatedStateResponse{
		Topology: &TopologyResponse{
			RegionID: c.regionID,
			Nodes:    nodes,
		},
		Grids: grids,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GET /topology - Return node positions
func (c *Controller) handleTopology(w http.ResponseWriter, r *http.Request) {
	c.nodesMu.RLock()
	nodes := make(map[int]*NodeInfo)
	for k, v := range c.nodes {
		nodes[k] = v
	}
	c.nodesMu.RUnlock()

	response := TopologyResponse{
		RegionID: c.regionID,
		Nodes:    nodes,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GET /metrics - Simple system metrics
func (c *Controller) handleMetrics(w http.ResponseWriter, r *http.Request) {
	c.nodesMu.RLock()
	nodeCount := len(c.nodes)
	c.nodesMu.RUnlock()

	c.stateMu.RLock()
	stateCount := len(c.gridStates)
	c.stateMu.RUnlock()

	c.generationMu.RLock()
	currentGen := c.currentGeneration
	c.generationMu.RUnlock()

	metrics := map[string]interface{}{
		"timestamp":    time.Now().Unix(),
		"regionId":     c.regionID,
		"nodes":        nodeCount,
		"activeGrids":  stateCount,
		"generation":   currentGen,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

// GET /health - Health check
func (c *Controller) handleHealth(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":   "healthy",
		"regionId": c.regionID,
		"nodes":    len(c.nodes),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

func main() {
	controller := NewController()

	r := mux.NewRouter()

	// Core endpoints for simplified architecture
	r.HandleFunc("/register", controller.handleRegister).Methods("POST")
	r.HandleFunc("/state/{position}", controller.handleStateUpdate).Methods("POST")
	r.HandleFunc("/halo/{position}", controller.handleHaloRequest).Methods("GET")
	r.HandleFunc("/generation", controller.handleGeneration).Methods("GET")
	r.HandleFunc("/aggregated-state", controller.handleAggregatedState).Methods("GET")
	r.HandleFunc("/topology", controller.handleTopology).Methods("GET")
	r.HandleFunc("/metrics", controller.handleMetrics).Methods("GET")
	r.HandleFunc("/health", controller.handleHealth).Methods("GET")

	// Legacy API mapping for web interface
	r.HandleFunc("/api/grid", controller.handleAggregatedState).Methods("GET")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	log.Printf("Simplified Game of Life Controller starting on port %s (Region: %s)", port, controller.regionID)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), r))
}