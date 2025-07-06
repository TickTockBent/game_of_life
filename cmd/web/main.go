package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
)

type WebServer struct {
	controllerURL string
}

type CellClickRequest struct {
	GlobalX int `json:"globalX"`
	GlobalY int `json:"globalY"`
	Alive   bool `json:"alive"`
}

func NewWebServer() *WebServer {
	controllerURL := os.Getenv("CONTROLLER_URL")
	if controllerURL == "" {
		controllerURL = "http://localhost:9081"
	}

	return &WebServer{
		controllerURL: controllerURL,
	}
}

// Serve the main HTML page
func (w *WebServer) handleIndex(rw http.ResponseWriter, r *http.Request) {
	http.ServeFile(rw, r, "static/public/index.html")
}

// Proxy topology requests to controller
func (w *WebServer) handleTopology(rw http.ResponseWriter, r *http.Request) {
	resp, err := http.Get(w.controllerURL + "/topology")
	if err != nil {
		http.Error(rw, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(resp.StatusCode)
	
	// Copy response body
	buf := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			rw.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
}

// Get aggregated grid state from all nodes
func (w *WebServer) handleGridState(rw http.ResponseWriter, r *http.Request) {
	// First get topology to know all nodes
	topologyResp, err := http.Get(w.controllerURL + "/topology")
	if err != nil {
		http.Error(rw, "Failed to get topology", http.StatusServiceUnavailable)
		return
	}
	defer topologyResp.Body.Close()

	var topology map[string]interface{}
	if err := json.NewDecoder(topologyResp.Body).Decode(&topology); err != nil {
		http.Error(rw, "Failed to parse topology", http.StatusInternalServerError)
		return
	}

	nodes, ok := topology["nodes"].(map[string]interface{})
	if !ok {
		http.Error(rw, "Invalid topology format", http.StatusInternalServerError)
		return
	}

	// Collect state from all nodes in parallel
	gridStates := make(map[string]interface{})
	type nodeResult struct {
		position string
		state    map[string]interface{}
		err      error
	}
	
	resultChan := make(chan nodeResult, len(nodes))
	
	// Start goroutines for each node
	for posStr, nodeInterface := range nodes {
		go func(pos string, nodeIface interface{}) {
			nodeMap, ok := nodeIface.(map[string]interface{})
			if !ok {
				resultChan <- nodeResult{pos, nil, fmt.Errorf("invalid node format")}
				return
			}

			endpoint, ok := nodeMap["endpoint"].(string)
			if !ok {
				resultChan <- nodeResult{pos, nil, fmt.Errorf("invalid endpoint")}
				return
			}

			// Get state from this node with timeout
			client := &http.Client{Timeout: 2 * time.Second}
			stateResp, err := client.Get(endpoint + "/state")
			if err != nil {
				log.Printf("Failed to get state from %s: %v", endpoint, err)
				resultChan <- nodeResult{pos, nil, err}
				return
			}
			defer stateResp.Body.Close()

			var nodeState map[string]interface{}
			if err := json.NewDecoder(stateResp.Body).Decode(&nodeState); err != nil {
				log.Printf("Failed to parse state from %s: %v", endpoint, err)
				resultChan <- nodeResult{pos, nil, err}
				return
			}

			resultChan <- nodeResult{pos, nodeState, nil}
		}(posStr, nodeInterface)
	}
	
	// Collect results
	for i := 0; i < len(nodes); i++ {
		result := <-resultChan
		if result.err == nil && result.state != nil {
			gridStates[result.position] = result.state
		}
	}

	response := map[string]interface{}{
		"topology": topology,
		"grids":    gridStates,
	}

	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(response)
}

// Handle cell click - forward to appropriate node
func (w *WebServer) handleCellClick(rw http.ResponseWriter, r *http.Request) {
	var req CellClickRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Cell click: global (%d, %d)", req.GlobalX, req.GlobalY)

	// Calculate which node owns this cell (simplified - assumes 7x7 grids)
	// Global coordinates to grid position
	gridX := req.GlobalX / 7
	gridY := req.GlobalY / 7
	position := gridY*10 + gridX // 10x10 grid of nodes

	// Local coordinates within the node's grid
	localX := req.GlobalX % 7
	localY := req.GlobalY % 7

	log.Printf("Calculated: grid (%d, %d) -> position %d, local (%d, %d)", gridX, gridY, position, localX, localY)

	// Get node info from controller
	nodeResp, err := http.Get(fmt.Sprintf("%s/node/%d", w.controllerURL, position))
	if err != nil {
		http.Error(rw, "Node not found", http.StatusNotFound)
		return
	}
	defer nodeResp.Body.Close()

	if nodeResp.StatusCode != 200 {
		http.Error(rw, "Node not found", http.StatusNotFound)
		return
	}

	var nodeInfo map[string]interface{}
	if err := json.NewDecoder(nodeResp.Body).Decode(&nodeInfo); err != nil {
		http.Error(rw, "Failed to parse node info", http.StatusInternalServerError)
		return
	}

	endpoint, ok := nodeInfo["endpoint"].(string)
	if !ok {
		http.Error(rw, "Invalid node endpoint", http.StatusInternalServerError)
		return
	}

	log.Printf("Forwarding to endpoint: %s", endpoint)

	// Forward cell update to the node
	cellUpdate := map[string]interface{}{
		"x":     localX,
		"y":     localY,
		"alive": req.Alive,
	}

	jsonBody, _ := json.Marshal(cellUpdate)
	updateResp, err := http.Post(endpoint+"/cell", "application/json", 
		bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Printf("Failed to post to %s: %v", endpoint, err)
		http.Error(rw, "Failed to update cell", http.StatusServiceUnavailable)
		return
	}
	defer updateResp.Body.Close()

	log.Printf("Cell update response: %d", updateResp.StatusCode)
	rw.WriteHeader(updateResp.StatusCode)
}

func main() {
	webServer := NewWebServer()

	r := mux.NewRouter()

	// Serve static files
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", 
		http.FileServer(http.Dir("static/public/"))))
	
	// API endpoints
	r.HandleFunc("/api/topology", webServer.handleTopology).Methods("GET")
	r.HandleFunc("/api/grid", webServer.handleGridState).Methods("GET")
	r.HandleFunc("/api/click", webServer.handleCellClick).Methods("POST")
	
	// Main page
	r.HandleFunc("/", webServer.handleIndex).Methods("GET")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Game of Life Web Server starting on port %s\n", port)
	log.Printf("Controller URL: %s\n", webServer.controllerURL)
	log.Printf("Access the web interface at: http://0.0.0.0:%s\n", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf("0.0.0.0:%s", port), r))
}