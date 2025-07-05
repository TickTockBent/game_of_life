package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/ticktockbent/game_of_life/pkg/controller"
)

type Controller struct {
	topology *controller.Topology
	regionID string
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
		regionID = "default"
	}

	return &Controller{
		topology: controller.NewTopology(),
		regionID: regionID,
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
	
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	
	log.Printf("Game of Life Controller starting on port %s (Region: %s)\n", port, controller.regionID)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), r))
}