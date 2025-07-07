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
)

type WebServer struct {
	controllerURL string
	httpClient    *http.Client
}

type CellClickRequest struct {
	GlobalX int `json:"globalX"`
	GlobalY int `json:"globalY"`
	Alive   bool `json:"alive"`
}

func NewWebServer() *WebServer {
	// Always use external controller URL for global connectivity
	controllerURL := "https://gameoflife-api.ticktockbent.com"

	// Create reusable HTTP client with connection pooling
	httpClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	return &WebServer{
		controllerURL: controllerURL,
		httpClient:    httpClient,
	}
}

// Serve the main HTML page
func (w *WebServer) handleIndex(rw http.ResponseWriter, r *http.Request) {
	http.ServeFile(rw, r, "static/public/index.html")
}

// Proxy topology requests to controller
func (w *WebServer) handleTopology(rw http.ResponseWriter, r *http.Request) {
	resp, err := w.httpClient.Get(w.controllerURL + "/topology")
	if err != nil {
		http.Error(rw, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(resp.StatusCode)
	
	// Use io.Copy for efficient streaming
	io.Copy(rw, resp.Body)
}

// Get aggregated grid state from all nodes
func (w *WebServer) handleGridState(rw http.ResponseWriter, r *http.Request) {
	// First get topology to know all nodes
	topologyResp, err := w.httpClient.Get(w.controllerURL + "/topology")
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
			stateResp, err := w.httpClient.Get(endpoint + "/state")
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

// Handle grid click - randomize the clicked grid
func (w *WebServer) handleCellClick(rw http.ResponseWriter, r *http.Request) {
	var req CellClickRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Grid click: global (%d, %d)", req.GlobalX, req.GlobalY)

	// Get topology first to find the correct node mapping
	topologyResp, err := w.httpClient.Get(w.controllerURL + "/topology")
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

	// Calculate which grid section was clicked
	gridX := req.GlobalX / 7
	gridY := req.GlobalY / 7

	log.Printf("Randomizing grid section (%d, %d)", gridX, gridY)

	// Find the node that matches this grid position
	var targetNode map[string]interface{}
	
	for _, nodeInterface := range nodes {
		if nodeMap, ok := nodeInterface.(map[string]interface{}); ok {
			if position, ok := nodeMap["position"].(map[string]interface{}); ok {
				if row, rowOk := position["row"].(float64); rowOk {
					if col, colOk := position["col"].(float64); colOk {
						if int(row) == gridY && int(col) == gridX {
							targetNode = nodeMap
							break
						}
					}
				}
			}
		}
	}

	if targetNode == nil {
		log.Printf("No node found for grid position (%d, %d)", gridX, gridY)
		http.Error(rw, "No node found for that position", http.StatusNotFound)
		return
	}

	// Use the found node directly
	endpoint, ok := targetNode["endpoint"].(string)
	if !ok {
		http.Error(rw, "Invalid node endpoint", http.StatusInternalServerError)
		return
	}

	podId, _ := targetNode["podId"].(string)
	log.Printf("Randomizing grid for node %s at endpoint: %s", podId, endpoint)

	// Send randomize command to the specific node
	randomizeResp, err := w.httpClient.Post(endpoint+"/randomize", "application/json", nil)
	if err != nil {
		log.Printf("Failed to randomize %s: %v", podId, err)
		http.Error(rw, "Failed to randomize grid", http.StatusServiceUnavailable)
		return
	}
	defer randomizeResp.Body.Close()

	if randomizeResp.StatusCode == 200 {
		log.Printf("Successfully randomized grid for %s", podId)
		rw.WriteHeader(http.StatusOK)
	} else {
		log.Printf("Randomize failed for %s: %d", podId, randomizeResp.StatusCode)
		http.Error(rw, "Failed to randomize grid", http.StatusServiceUnavailable)
	}
}

// Handle randomize all - send randomize command to all nodes
func (w *WebServer) handleRandomizeAll(rw http.ResponseWriter, r *http.Request) {
	log.Printf("Randomizing all nodes...")
	
	// First get topology to know all nodes
	topologyResp, err := w.httpClient.Get(w.controllerURL + "/topology")
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

	// Send randomize command to all nodes with slight staggering to avoid overwhelming
	type nodeResult struct {
		nodeId string
		err    error
	}
	
	resultChan := make(chan nodeResult, len(nodes))
	
	// Stagger the requests slightly to avoid overwhelming the cluster
	nodeCount := 0
	for posStr, nodeInterface := range nodes {
		// Add a small delay between requests
		if nodeCount > 0 {
			time.Sleep(100 * time.Millisecond)
		}
		nodeCount++
		
		go func(pos string, nodeIface interface{}) {
			nodeMap, ok := nodeIface.(map[string]interface{})
			if !ok {
				resultChan <- nodeResult{pos, fmt.Errorf("invalid node format")}
				return
			}

			endpoint, ok := nodeMap["endpoint"].(string)
			if !ok {
				resultChan <- nodeResult{pos, fmt.Errorf("invalid endpoint")}
				return
			}

			podId, _ := nodeMap["podId"].(string)

			// Send randomize command with timeout
			randomizeResp, err := w.httpClient.Post(endpoint+"/randomize", "application/json", nil)
			if err != nil {
				log.Printf("Failed to randomize %s: %v", podId, err)
				resultChan <- nodeResult{podId, err}
				return
			}
			defer randomizeResp.Body.Close()

			if randomizeResp.StatusCode == 200 {
				log.Printf("Successfully randomized %s", podId)
				resultChan <- nodeResult{podId, nil}
			} else {
				log.Printf("Randomize failed for %s: %d", podId, randomizeResp.StatusCode)
				resultChan <- nodeResult{podId, fmt.Errorf("status %d", randomizeResp.StatusCode)}
			}
		}(posStr, nodeInterface)
	}
	
	// Collect results
	successCount := 0
	for i := 0; i < len(nodes); i++ {
		result := <-resultChan
		if result.err == nil {
			successCount++
		}
	}

	log.Printf("Randomized %d/%d nodes successfully", successCount, len(nodes))
	
	response := map[string]interface{}{
		"success": successCount,
		"total":   len(nodes),
	}

	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(response)
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
	r.HandleFunc("/api/randomize", webServer.handleRandomizeAll).Methods("POST")
	
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