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
	"github.com/ticktockbent/game_of_life/pkg/controller"
)

type Controller struct {
	topology *controller.Topology
	regionID string
	// Request tracking for overload detection
	requestCounter  int64
	registrations   int64
	neighborLookups int64
	healthChecks    int64
	mu              sync.Mutex
	lastStatsLog    time.Time
}

type RegisterRequest struct {
	PodID    string `json:"podId"`
	Endpoint string `json:"endpoint"`
}

type RegisterResponse struct {
	Position  int                  `json:"position"`
	Neighbors map[string]int       `json:"neighbors"`
}

type TopologyResponse struct {
	RegionID string                         `json:"regionId"`
	Nodes    map[int]*controller.NodeInfo   `json:"nodes"`
}

func NewController() *Controller {
	regionID := os.Getenv("REGION_ID")
	if regionID == "" {
		regionID = "k3s-cluster"
	}

	c := &Controller{
		topology: controller.NewTopology(),
		regionID: regionID,
		lastStatsLog: time.Now(),
	}
	
	// Start request statistics logging
	go c.statsLoop()
	
	// Start aggressive health checking
	go c.healthCheckLoop()
	
	// Start barrier sync coordinator
	go c.barrierSyncLoop()
	
	return c
}

// healthCheckLoop continuously checks all nodes and removes unhealthy ones
func (c *Controller) healthCheckLoop() {
	ticker := time.NewTicker(3 * time.Second) // Less aggressive checking for external connectivity
	defer ticker.Stop()
	
	for range ticker.C {
		c.checkAllNodesHealth()
	}
}

// checkAllNodesHealth checks each node and removes unresponsive ones
func (c *Controller) checkAllNodesHealth() {
	nodes := c.topology.GetAllNodes()
	
	for position, node := range nodes {
		if !c.isNodeHealthy(node) {
			log.Printf("Removing unhealthy node %s at position %d", node.PodID, position)
			c.topology.UnregisterNode(position)
		}
	}
}

// isNodeHealthy checks if a node responds to health check within timeout
func (c *Controller) isNodeHealthy(node *controller.NodeInfo) bool {
	client := &http.Client{Timeout: 3 * time.Second} // More reasonable timeout for external routing
	
	resp, err := client.Get(node.Endpoint + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	
	return resp.StatusCode == 200
}

// barrierSyncLoop coordinates barrier synchronization by polling engines
func (c *Controller) barrierSyncLoop() {
	ticker := time.NewTicker(50 * time.Millisecond) // Poll frequently for responsiveness
	defer ticker.Stop()
	
	for range ticker.C {
		nodes := c.topology.GetAllNodes()
		if len(nodes) == 0 {
			continue
		}
		
		// Poll all engines to check if they're ready
		readyCount := c.checkAllEnginesReady(nodes)
		
		if readyCount == len(nodes) {
			log.Printf("All %d nodes ready - broadcasting step", len(nodes))
			c.broadcastStep(nodes)
		}
	}
}

// checkAllEnginesReady polls all engines' /ready endpoints
func (c *Controller) checkAllEnginesReady(nodes map[int]*controller.NodeInfo) int {
	readyCount := 0
	
	for _, node := range nodes {
		if c.isEngineReady(node) {
			readyCount++
		}
	}
	
	return readyCount
}

// isEngineReady checks if a specific engine is ready
func (c *Controller) isEngineReady(node *controller.NodeInfo) bool {
	client := &http.Client{Timeout: 1 * time.Second}
	
	resp, err := client.Get(node.Endpoint + "/ready")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return false
	}
	
	var readyResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&readyResp); err != nil {
		return false
	}
	
	ready, ok := readyResp["ready"].(bool)
	return ok && ready
}

// broadcastStep sends step command to all engines
func (c *Controller) broadcastStep(nodes map[int]*controller.NodeInfo) {
	for _, node := range nodes {
		go func(n *controller.NodeInfo) {
			client := &http.Client{Timeout: 2 * time.Second}
			resp, err := client.Post(n.Endpoint+"/step", "application/json", nil)
			if err != nil {
				log.Printf("Failed to step node %s: %v", n.PodID, err)
				return
			}
			defer resp.Body.Close()
			
			if resp.StatusCode != 200 {
				log.Printf("Step failed for node %s: %d", n.PodID, resp.StatusCode)
			}
		}(node)
	}
}

// requestLoggingMiddleware logs request patterns and detects overload
func (c *Controller) requestLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		atomic.AddInt64(&c.requestCounter, 1)
		
		// Track specific endpoint types
		switch {
		case r.URL.Path == "/register":
			atomic.AddInt64(&c.registrations, 1)
		case r.URL.Path == "/health":
			atomic.AddInt64(&c.healthChecks, 1)
		case len(r.URL.Path) > 10 && r.URL.Path[:10] == "/neighbors":
			atomic.AddInt64(&c.neighborLookups, 1)
		}
		
		next.ServeHTTP(w, r)
		
		duration := time.Since(start)
		if duration > 500*time.Millisecond {
			log.Printf("SLOW REQUEST: %s %s took %v", r.Method, r.URL.Path, duration)
		}
	})
}

// statsLoop logs request statistics every 30 seconds
func (c *Controller) statsLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for range ticker.C {
		c.logRequestStats()
	}
}

// logRequestStats prints current request load and detects overload patterns
func (c *Controller) logRequestStats() {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	now := time.Now()
	duration := now.Sub(c.lastStatsLog)
	c.lastStatsLog = now
	
	totalReqs := atomic.LoadInt64(&c.requestCounter)
	regs := atomic.LoadInt64(&c.registrations)
	health := atomic.LoadInt64(&c.healthChecks)
	neighbors := atomic.LoadInt64(&c.neighborLookups)
	
	// Calculate rates per minute
	minutes := duration.Minutes()
	totalRate := float64(totalReqs) / minutes
	regRate := float64(regs) / minutes
	healthRate := float64(health) / minutes
	neighborRate := float64(neighbors) / minutes
	
	log.Printf("REQUEST STATS: %.1f req/min (%.1f reg/min, %.1f health/min, %.1f neighbor/min) - %d nodes", 
		totalRate, regRate, healthRate, neighborRate, len(c.topology.GetAllNodes()))
	
	// Detect potential overload patterns
	if totalRate > 300 {
		log.Printf("WARNING: High request rate detected (%.1f req/min)", totalRate)
	}
	if regRate > 20 {
		log.Printf("WARNING: High registration churn detected (%.1f reg/min)", regRate)
	}
	
	// Reset counters
	atomic.StoreInt64(&c.requestCounter, 0)
	atomic.StoreInt64(&c.registrations, 0)
	atomic.StoreInt64(&c.healthChecks, 0)
	atomic.StoreInt64(&c.neighborLookups, 0)
}

func (c *Controller) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	
	position, err := c.topology.RegisterNode(req.PodID, req.Endpoint)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	
	response := RegisterResponse{
		Position:  position,
		Neighbors: controller.GetNeighborPositions(position),
	}
	
	log.Printf("Registered node %s at position %d", req.PodID, position)
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (c *Controller) handleGetTopology(w http.ResponseWriter, r *http.Request) {
	response := TopologyResponse{
		RegionID: c.regionID,
		Nodes:    c.topology.GetAllNodes(),
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (c *Controller) handleGetNode(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	position, err := strconv.Atoi(vars["position"])
	if err != nil {
		http.Error(w, "Invalid position", http.StatusBadRequest)
		return
	}
	
	node, exists := c.topology.GetNode(position)
	if !exists {
		http.Error(w, "Node not found at position", http.StatusNotFound)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(node)
}

func (c *Controller) handleUnregister(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	position, err := strconv.Atoi(vars["position"])
	if err != nil {
		http.Error(w, "Invalid position", http.StatusBadRequest)
		return
	}
	
	c.topology.UnregisterNode(position)
	log.Printf("Unregistered node at position %d", position)
	
	w.WriteHeader(http.StatusOK)
}

func (c *Controller) handleGetNeighbors(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	position, err := strconv.Atoi(vars["position"])
	if err != nil {
		http.Error(w, "Invalid position", http.StatusBadRequest)
		return
	}
	
	neighbors := c.topology.GetNeighbors(position)
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(neighbors)
}

func (c *Controller) handleHealth(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":   "healthy",
		"regionId": c.regionID,
		"nodes":    len(c.topology.GetAllNodes()),
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

// Old barrier sync handlers removed - now using polling approach

func main() {
	controller := NewController()
	
	r := mux.NewRouter()
	
	// Add request logging middleware
	r.Use(controller.requestLoggingMiddleware)
	
	// Registration endpoints
	r.HandleFunc("/register", controller.handleRegister).Methods("POST")
	r.HandleFunc("/node/{position}", controller.handleUnregister).Methods("DELETE")
	
	// Discovery endpoints
	r.HandleFunc("/topology", controller.handleGetTopology).Methods("GET")
	r.HandleFunc("/node/{position}", controller.handleGetNode).Methods("GET")
	r.HandleFunc("/neighbors/{position}", controller.handleGetNeighbors).Methods("GET")
	
	// Health check
	r.HandleFunc("/health", controller.handleHealth).Methods("GET")
	
	// Removed old barrier sync endpoints - now using polling
	
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	
	log.Printf("Game of Life Controller starting on port %s (Region: %s)\n", port, controller.regionID)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), r))
}