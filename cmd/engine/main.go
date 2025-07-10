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
	grid      *gameoflife.Grid
	nodeID    string
	controllerURL string
	position  int
	registered bool
	httpClient *http.Client
	stopChan   chan struct{}
	stepChan   chan struct{} // Channel to receive step signals
	failedPushes int         // Count of consecutive failed state pushes
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
		stepChan:      make(chan struct{}, 10), // Buffered channel for step signals
	}

	// Randomize initial state
	engine.grid.RandomSeed(0.3)

	return engine
}

// Start begins the engine lifecycle
func (e *Engine) Start() {
	log.Printf("Starting engine %s with router URL: %s", e.nodeID, e.controllerURL)
	
	// 1. Register with controller
	e.register()
	
	// 2. Start autonomous game loop
	go e.gameLoop()
	
	// 3. Start HTTP server for health checks
	go e.startHTTPServer()
}

// register with router to get position (router forwards to controller)
func (e *Engine) register() {
	for !e.registered {
		log.Printf("Attempting registration to %s/register", e.controllerURL)
		
		// Get pod IP for endpoint registration
		podIP := os.Getenv("POD_IP")
		if podIP == "" {
			podIP = "localhost" // fallback for testing
		}
		
		reqData := map[string]string{
			"podId":    e.nodeID,
			"endpoint": fmt.Sprintf("http://%s:8080", podIP),
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
			
			// Get current controller generation and sync with it
			if controllerGen, err := e.getControllerGeneration(); err == nil {
				e.grid.Generation = controllerGen
				log.Printf("Synced to controller generation %d", controllerGen)
			}
			
			// Push initial state (signals readiness for current generation)
			e.pushState()
		}
	}
}

// gameLoop runs the barrier-synchronized Conway's Game of Life
func (e *Engine) gameLoop() {
	log.Printf("Starting barrier sync game loop for engine %s", e.nodeID)
	
	for {
		select {
		case <-e.stopChan:
			return
		case <-e.stepChan:
			if e.registered {
				// Execute synchronized step
				e.executeStep()
			}
		default:
			if !e.registered {
				time.Sleep(1 * time.Second) // Wait for registration
			} else {
				// Wait for step signal - no busy loop
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
}

// executeStep performs one synchronized game iteration when signaled by controller
func (e *Engine) executeStep() {
	log.Printf("Engine %s executing step", e.nodeID)
	
	// 1. Get current controller generation
	controllerGen, err := e.getControllerGeneration()
	if err != nil {
		log.Printf("Failed to get controller generation: %v", err)
		return
	}
	
	// 2. Get halo data from controller
	_, err = e.getHalo()
	if err != nil {
		log.Printf("Failed to get halo: %v", err)
		return
	}
	
	// 3. Apply halo to grid (TODO: implement halo application)
	// For now, just compute next generation normally
	e.grid.ComputeNextGeneration()
	e.grid.CommitNextGeneration()
	
	// 4. Sync our generation with controller
	e.grid.Generation = controllerGen
	
	// 5. Push new state to controller (this signals readiness for next generation)
	e.pushState()
	
	log.Printf("Engine %s completed step for generation %d", e.nodeID, controllerGen)
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

// getControllerGeneration fetches current generation from router
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
		e.failedPushes++
		e.checkReregistration()
		return
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		log.Printf("State push rejected: %d", resp.StatusCode)
		e.failedPushes++
		e.checkReregistration()
		return
	}
	
	// Success - reset failure counter
	e.failedPushes = 0
}

// checkReregistration triggers re-registration if too many pushes have failed
func (e *Engine) checkReregistration() {
	if e.failedPushes >= 3 {
		log.Printf("Engine %s failed %d consecutive state pushes, re-registering", e.nodeID, e.failedPushes)
		e.registered = false
		e.position = -1
		e.failedPushes = 0
		// Registration will happen in the next game loop iteration
	}
}

// startHTTPServer provides health check and step signal endpoints
func (e *Engine) startHTTPServer() {
	r := mux.NewRouter()
	r.HandleFunc("/health", e.handleHealth).Methods("GET")
	r.HandleFunc("/step", e.handleStep).Methods("POST")
	
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

// handleStep receives step signal from controller
func (e *Engine) handleStep(w http.ResponseWriter, r *http.Request) {
	if !e.registered {
		http.Error(w, "Engine not registered", http.StatusServiceUnavailable)
		return
	}
	
	// Send step signal to game loop
	select {
	case e.stepChan <- struct{}{}:
		w.WriteHeader(http.StatusOK)
		log.Printf("Engine %s received step signal", e.nodeID)
	default:
		// Channel full, step already queued
		w.WriteHeader(http.StatusOK)
		log.Printf("Engine %s step signal already queued", e.nodeID)
	}
}

func main() {
	engine := NewEngine()
	engine.Start()
	
	// Keep main alive
	select {}
}