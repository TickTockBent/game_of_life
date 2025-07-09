package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/ticktockbent/game_of_life/pkg/gameoflife"
)

type Engine struct {
	grid          *gameoflife.Grid
	nodeID        string
	controllerURL string
	position      int
	registered    bool
	httpClient    *http.Client
	stopChan      chan struct{}
}

type HaloResponse struct {
	HaloCells [9][9]bool `json:"haloCells"`
}

func NewEngine() *Engine {
	// Get unique pod identifier
	podName := os.Getenv("POD_NAME")
	var nodeID string
	if podName != "" && len(podName) >= 5 {
		nodeID = podName[len(podName)-5:]
	} else {
		nodeID = "test"
	}

	// Use external controller URL
	controllerURL := os.Getenv("CONTROLLER_URL")
	if controllerURL == "" {
		controllerURL = "https://gameoflife-api.ticktockbent.com"
	}

	log.Printf("DEBUG: Creating HTTP client with 5s timeout")
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
			DisableKeepAlives:   true,  // Theory 2: Disable connection reuse
			MaxIdleConns:        0,     // Theory 2: No connection pooling
			IdleConnTimeout:     0,     // Theory 2: No idle connections
			TLSHandshakeTimeout: 3 * time.Second, // Theory 1: Longer TLS timeout
			DialContext: (&net.Dialer{
				Timeout: 3 * time.Second, // Theory 3: Longer dial timeout
			}).DialContext,
		},
	}

	engine := &Engine{
		grid:          gameoflife.NewGrid(),
		nodeID:        nodeID,
		controllerURL: controllerURL,
		position:      -1,
		httpClient:    httpClient,
		stopChan:      make(chan struct{}),
	}

	// Randomize initial state
	engine.grid.RandomSeed(0.3)

	return engine
}

// Start begins the engine lifecycle
func (e *Engine) Start() {
	log.Printf("Starting engine %s with controller URL: %s", e.nodeID, e.controllerURL)
	
	// 1. Register with controller
	e.register()
	
	// 2. Start autonomous game loop
	go e.gameLoop()
	
	// 3. Start HTTP server for health checks
	go e.startHTTPServer()
}

// register with controller to get position
func (e *Engine) register() {
	for !e.registered {
		log.Printf("Attempting registration to %s/register", e.controllerURL)
		
		reqData := map[string]string{
			"podId":    e.nodeID,
			"endpoint": "http://placeholder:8080",
		}
		
		jsonData, _ := json.Marshal(reqData)
		start := time.Now()
		resp, err := e.httpClient.Post(e.controllerURL+"/register", "application/json", strings.NewReader(string(jsonData)))
		elapsed := time.Since(start)
		if err != nil {
			log.Printf("Registration failed after %v: %v (Type: %T)", elapsed, err, err)
			if netErr, ok := err.(net.Error); ok {
				log.Printf("DEBUG: Network error during registration - Timeout: %v, Temporary: %v", netErr.Timeout(), netErr.Temporary())
			}
			time.Sleep(2 * time.Second)
			continue
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != 200 {
			log.Printf("Registration rejected: %d", resp.StatusCode)
			time.Sleep(2 * time.Second)
			continue
		}
		
		var regResp map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
			log.Printf("Failed to parse registration response: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		
		if pos, ok := regResp["position"].(float64); ok {
			e.position = int(pos)
			e.registered = true
			log.Printf("Registered at position %d", e.position)
			
			// Push initial state
			e.pushState()
		}
	}
}

// gameLoop runs the autonomous Conway's Game of Life
func (e *Engine) gameLoop() {
	for {
		select {
		case <-e.stopChan:
			return
		default:
			if e.registered {
				stepped := e.step()
				if !stepped {
					// We're caught up, wait before trying again
					time.Sleep(100 * time.Millisecond)
				}
				// If we stepped, immediately try again (catch-up mode)
			} else {
				time.Sleep(1 * time.Second) // Wait for registration
			}
		}
	}
}

// step performs one game iteration, returns true if stepped, false if caught up
func (e *Engine) step() bool {
	// 1. Check if we should step based on controller generation
	controllerGen, err := e.getControllerGeneration()
	if err != nil {
		log.Printf("Failed to get controller generation: %v", err)
		return false
	}
	
	// Don't step if we're already at or ahead of controller generation
	if e.grid.Generation >= controllerGen {
		return false // Caught up, no step needed
	}
	
	// 2. Get halo data from controller
	_, err = e.getHalo()
	if err != nil {
		log.Printf("Failed to get halo: %v", err)
		return false
	}
	
	// 3. Apply halo to grid (TODO: implement halo application)
	// For now, just compute next generation normally
	e.grid.ComputeNextGeneration()
	e.grid.CommitNextGeneration()
	
	// 4. Push new state to controller
	e.pushState()
	
	return true // Successfully stepped
}

// getHalo fetches surrounding cells from controller
func (e *Engine) getHalo() ([9][9]bool, error) {
	var halo [9][9]bool
	
	url := fmt.Sprintf("%s/halo/%d", e.controllerURL, e.position)
	resp, err := e.httpClient.Get(url)
	if err != nil {
		return halo, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return halo, fmt.Errorf("status %d", resp.StatusCode)
	}
	
	var haloResp HaloResponse
	err = json.NewDecoder(resp.Body).Decode(&haloResp)
	return haloResp.HaloCells, err
}

// getControllerGeneration fetches current generation from controller
func (e *Engine) getControllerGeneration() (int, error) {
	url := fmt.Sprintf("%s/generation", e.controllerURL)
	log.Printf("DEBUG: Attempting to GET %s", url)
	
	start := time.Now()
	resp, err := e.httpClient.Get(url)
	elapsed := time.Since(start)
	
	if err != nil {
		log.Printf("DEBUG: HTTP GET failed after %v: %v (Type: %T)", elapsed, err, err)
		if netErr, ok := err.(net.Error); ok {
			log.Printf("DEBUG: Network error - Timeout: %v, Temporary: %v", netErr.Timeout(), netErr.Temporary())
		}
		return 0, err
	}
	defer resp.Body.Close()

	log.Printf("DEBUG: HTTP GET succeeded after %v, status: %d", elapsed, resp.StatusCode)

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}

	var genResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return 0, err
	}

	if gen, ok := genResp["generation"].(float64); ok {
		return int(gen), nil
	}
	
	return 0, fmt.Errorf("invalid generation response")
}

// pushState sends current grid to controller
func (e *Engine) pushState() {
	// Convert grid to [][]bool
	gridState := make([][]bool, gameoflife.GridSize)
	for i := range gridState {
		gridState[i] = make([]bool, gameoflife.GridSize)
		for j := range gridState[i] {
			gridState[i][j] = bool(e.grid.Cells[i][j])
		}
	}
	
	stateData := map[string]interface{}{
		"grid":       gridState,
		"generation": e.grid.Generation,
	}
	
	jsonData, _ := json.Marshal(stateData)
	url := fmt.Sprintf("%s/state/%d", e.controllerURL, e.position)
	resp, err := e.httpClient.Post(url, "application/json", strings.NewReader(string(jsonData)))
	if err != nil {
		log.Printf("Failed to push state: %v", err)
		return
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		log.Printf("State push rejected: %d", resp.StatusCode)
	}
}

// startHTTPServer provides health check endpoint
func (e *Engine) startHTTPServer() {
	r := mux.NewRouter()
	r.HandleFunc("/health", e.handleHealth).Methods("GET")
	
	port := "8080"
	log.Printf("HTTP server starting on port %s", port)
	http.ListenAndServe(":"+port, r)
}

// handleHealth returns simple health check
func (e *Engine) handleHealth(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":     "healthy",
		"nodeId":     e.nodeID,
		"registered": e.registered,
		"position":   e.position,
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

func main() {
	engine := NewEngine()
	engine.Start()
	
	// Keep main alive
	select {}
}