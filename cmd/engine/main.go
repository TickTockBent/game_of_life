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
	
	return engine
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
				e.grid.NextGeneration()
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
	
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	
	// Register with controller if configured
	if engine.distributedGrid != nil {
		if err := engine.distributedGrid.Register(engine.nodeID); err != nil {
			log.Printf("Failed to register with controller: %v", err)
			// Continue running in standalone mode
		} else {
			log.Printf("Registered with controller at position %d", engine.distributedGrid.GetPosition())
		}
	}
	
	log.Printf("Game of Life Engine starting on port %s (Node: %s)\n", port, engine.nodeID)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), r))
}