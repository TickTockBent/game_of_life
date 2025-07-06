package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/ticktockbent/game_of_life/pkg/controller"
)

type Controller struct {
	topology *controller.Topology
	regionID string
	stepChannel chan bool // Channel to broadcast step commands
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
		stepChannel: make(chan bool, 100), // Buffered for multiple subscribers
	}
	
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

// barrierSyncLoop coordinates barrier synchronization across all nodes
func (c *Controller) barrierSyncLoop() {
	ticker := time.NewTicker(100 * time.Millisecond) // Check readiness frequently
	defer ticker.Stop()
	
	for range ticker.C {
		if c.topology.AreAllNodesReady() {
			// All nodes ready - broadcast step command
			readyCount, totalCount := c.topology.GetReadyCount()
			if totalCount > 0 {
				log.Printf("All %d nodes ready - broadcasting step", totalCount)
				
				// Reset ready status for next cycle
				c.topology.ResetReadyStatus()
				
				// Broadcast step to all waiting nodes
				select {
				case c.stepChannel <- true:
				default:
					// Channel full, skip this step
				}
			}
		}
	}
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

// handleReady allows nodes to signal they are ready for the next step
func (c *Controller) handleReady(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	position, err := strconv.Atoi(vars["position"])
	if err != nil {
		http.Error(w, "Invalid position", http.StatusBadRequest)
		return
	}
	
	c.topology.MarkNodeReady(position)
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ready",
		"position": position,
	})
}

// handleWaitForStep blocks until all nodes are ready and step is broadcast
func (c *Controller) handleWaitForStep(w http.ResponseWriter, r *http.Request) {
	// Wait for step signal with timeout
	select {
	case <-c.stepChannel:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"command": "step",
			"timestamp": time.Now().Unix(),
		})
	case <-time.After(10 * time.Second): // Timeout after 10 seconds
		http.Error(w, "Timeout waiting for step", http.StatusRequestTimeout)
	}
}

func main() {
	controller := NewController()
	
	r := mux.NewRouter()
	
	// Registration endpoints
	r.HandleFunc("/register", controller.handleRegister).Methods("POST")
	r.HandleFunc("/node/{position}", controller.handleUnregister).Methods("DELETE")
	
	// Discovery endpoints
	r.HandleFunc("/topology", controller.handleGetTopology).Methods("GET")
	r.HandleFunc("/node/{position}", controller.handleGetNode).Methods("GET")
	r.HandleFunc("/neighbors/{position}", controller.handleGetNeighbors).Methods("GET")
	
	// Health check
	r.HandleFunc("/health", controller.handleHealth).Methods("GET")
	
	// Barrier synchronization endpoints
	r.HandleFunc("/ready/{position}", controller.handleReady).Methods("POST")
	r.HandleFunc("/wait-step", controller.handleWaitForStep).Methods("GET")
	
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	
	log.Printf("Game of Life Controller starting on port %s (Region: %s)\n", port, controller.regionID)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), r))
}