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

type PublicEngine struct {
	grid      *gameoflife.Grid
	nodeID    string
	displayName string         // User-provided display name
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

func NewPublicEngine() *PublicEngine {
	// Get display name from environment variable
	displayName := os.Getenv("DISPLAY_NAME")
	if displayName == "" {
		displayName = "Anonymous"
	}

	// Generate a unique nodeID (abbreviated UUID-like)
	nodeID := fmt.Sprintf("pub-%d", time.Now().Unix()%100000)

	// Use public controller URL
	controllerURL := os.Getenv("CONTROLLER_URL")
	if controllerURL == "" {
		controllerURL = "http://ticktockbent.com:8082"
	}

	log.Printf("Creating public engine with display name: %s, nodeID: %s", displayName, nodeID)
	
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
			DisableKeepAlives:   true,
			MaxIdleConns:        0,
			IdleConnTimeout:     0,
			TLSHandshakeTimeout: 3 * time.Second,
			DialContext: (&net.Dialer{
				Timeout: 3 * time.Second,
			}).DialContext,
		},
	}

	engine := &PublicEngine{
		grid:          gameoflife.NewGrid(),
		nodeID:        nodeID,
		displayName:   displayName,
		controllerURL: controllerURL,
		position:      -1,
		httpClient:    httpClient,
		stopChan:      make(chan struct{}),
		stepChan:      make(chan struct{}, 10),
	}

	// Randomize initial state
	engine.grid.RandomSeed(0.3)

	return engine
}

// Start begins the engine lifecycle
func (e *PublicEngine) Start() {
	log.Printf("Starting public engine %s (%s) with controller URL: %s", e.displayName, e.nodeID, e.controllerURL)
	
	// 1. Register with controller
	e.register()
	
	// 2. Start autonomous game loop
	go e.gameLoop()
	
	// 3. Start HTTP server for health checks
	go e.startHTTPServer()
}

// register with controller to get position
func (e *PublicEngine) register() {
	for !e.registered {
		log.Printf("Attempting registration to %s/register", e.controllerURL)
		
		// For public containers, we need to determine our external IP
		externalIP := e.getExternalIP()
		if externalIP == "" {
			log.Printf("Failed to determine external IP, using localhost")
			externalIP = "localhost"
		}
		
		reqData := map[string]string{
			"podId":       e.nodeID,
			"endpoint":    fmt.Sprintf("http://%s:8080", externalIP),
			"displayName": e.displayName, // Add display name to registration
		}
		
		jsonData, _ := json.Marshal(reqData)
		start := time.Now()
		resp, err := e.httpClient.Post(e.controllerURL+"/register", "application/json", strings.NewReader(string(jsonData)))
		elapsed := time.Since(start)
		if err != nil {
			log.Printf("Registration failed after %v: %v", elapsed, err)
			time.Sleep(5 * time.Second)
			continue
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != 200 {
			log.Printf("Registration rejected: %d", resp.StatusCode)
			time.Sleep(5 * time.Second)
			continue
		}
		
		var regResp map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
			log.Printf("Failed to parse registration response: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		
		if pos, ok := regResp["position"].(float64); ok {
			e.position = int(pos)
			e.registered = true
			log.Printf("Registered %s (%s) at position %d", e.displayName, e.nodeID, e.position)
			
			// Get current controller generation and sync with it
			if controllerGen, err := e.getControllerGeneration(); err == nil {
				e.grid.Generation = controllerGen
				log.Printf("Synced to controller generation %d", controllerGen)
			}
		} else {
			log.Printf("Invalid registration response: %+v", regResp)
			time.Sleep(5 * time.Second)
		}
	}
}

// getExternalIP attempts to determine the external IP address
func (e *PublicEngine) getExternalIP() string {
	// Try to get external IP from environment variable first
	if externalIP := os.Getenv("EXTERNAL_IP"); externalIP != "" {
		return externalIP
	}
	
	// Try to determine IP by connecting to external service
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Printf("Failed to determine external IP: %v", err)
		return ""
	}
	defer conn.Close()
	
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

// getControllerGeneration fetches current generation from controller
func (e *PublicEngine) getControllerGeneration() (int, error) {
	resp, err := e.httpClient.Get(e.controllerURL + "/generation")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	
	var genResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return 0, err
	}
	
	if gen, ok := genResp["generation"].(float64); ok {
		return int(gen), nil
	}
	
	return 0, fmt.Errorf("invalid generation response")
}

// gameLoop runs the main simulation loop with barrier synchronization
func (e *PublicEngine) gameLoop() {
	for {
		select {
		case <-e.stopChan:
			return
		case <-e.stepChan:
			// Received step signal from controller
			e.performStep()
		}
	}
}

// performStep executes one generation step
func (e *PublicEngine) performStep() {
	if !e.registered {
		return
	}
	
	// Fetch halo data for border cells
	haloData, err := e.fetchHaloData()
	if err != nil {
		log.Printf("Failed to fetch halo data: %v", err)
		// Continue without halo data - isolated stepping
	}
	
	// Apply halo data to grid borders
	if haloData != nil {
		// Extract edge data from 9x9 halo and apply to grid
		north := make([]bool, 7)
		south := make([]bool, 7)
		east := make([]bool, 7)
		west := make([]bool, 7)
		
		// Extract edges from halo data
		for i := 0; i < 7; i++ {
			north[i] = haloData.HaloCells[0][i+1]  // Top row
			south[i] = haloData.HaloCells[8][i+1]  // Bottom row
			west[i] = haloData.HaloCells[i+1][0]   // Left column
			east[i] = haloData.HaloCells[i+1][8]   // Right column
		}
		
		// Apply halo regions to grid
		e.grid.UpdateHaloRegion("north", north)
		e.grid.UpdateHaloRegion("south", south)
		e.grid.UpdateHaloRegion("east", east)
		e.grid.UpdateHaloRegion("west", west)
	}
	
	// Use the correct method name for stepping
	e.grid.NextGeneration()
	
	// Push updated state to controller
	if err := e.pushState(); err != nil {
		e.failedPushes++
		log.Printf("Failed to push state (%d consecutive failures): %v", e.failedPushes, err)
		
		// Re-register after 3 consecutive failures
		if e.failedPushes >= 3 {
			log.Printf("Re-registering after %d failed pushes", e.failedPushes)
			e.registered = false
			e.failedPushes = 0
			go e.register()
		}
	} else {
		e.failedPushes = 0 // Reset failure count on success
	}
}

// fetchHaloData gets border cell data from controller
func (e *PublicEngine) fetchHaloData() (*HaloResponse, error) {
	resp, err := e.httpClient.Get(fmt.Sprintf("%s/halo/%d", e.controllerURL, e.position))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("halo request failed: %d", resp.StatusCode)
	}
	
	var haloResp HaloResponse
	if err := json.NewDecoder(resp.Body).Decode(&haloResp); err != nil {
		return nil, err
	}
	
	return &haloResp, nil
}

// pushState sends current grid state to controller
func (e *PublicEngine) pushState() error {
	gridState, generation := e.grid.GetState()
	stateData := map[string]interface{}{
		"grid":       gridState,
		"generation": generation,
	}
	
	jsonData, _ := json.Marshal(stateData)
	resp, err := e.httpClient.Post(
		fmt.Sprintf("%s/state/%d", e.controllerURL, e.position),
		"application/json",
		strings.NewReader(string(jsonData)),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return fmt.Errorf("state push failed: %d", resp.StatusCode)
	}
	
	return nil
}

// startHTTPServer starts the HTTP server for health checks and step signals
func (e *PublicEngine) startHTTPServer() {
	r := mux.NewRouter()
	
	// Health check endpoint
	r.HandleFunc("/health", e.handleHealth).Methods("GET")
	
	// Step signal endpoint (called by controller for barrier sync)
	r.HandleFunc("/step", e.handleStep).Methods("POST")
	
	// Randomize endpoint
	r.HandleFunc("/randomize", e.handleRandomize).Methods("POST")
	
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	
	log.Printf("Public engine HTTP server starting on port %s", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), r))
}

// handleHealth returns engine health status
func (e *PublicEngine) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"status":     "healthy",
		"nodeId":     e.nodeID,
		"displayName": e.displayName,
		"position":   e.position,
		"registered": e.registered,
		"generation": e.grid.GetGeneration(),
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleStep receives step signals from controller
func (e *PublicEngine) handleStep(w http.ResponseWriter, r *http.Request) {
	// Send step signal to game loop
	select {
	case e.stepChan <- struct{}{}:
		w.WriteHeader(http.StatusOK)
	default:
		// Channel full, step already pending
		w.WriteHeader(http.StatusOK)
	}
}

// handleRandomize randomizes the grid
func (e *PublicEngine) handleRandomize(w http.ResponseWriter, r *http.Request) {
	e.grid.RandomSeed(0.3)
	log.Printf("Grid randomized for %s (%s)", e.displayName, e.nodeID)
	w.WriteHeader(http.StatusOK)
}

func main() {
	engine := NewPublicEngine()
	
	log.Printf("=== Public Game of Life Engine ===")
	log.Printf("Display Name: %s", engine.displayName)
	log.Printf("Node ID: %s", engine.nodeID)
	log.Printf("Controller: %s", engine.controllerURL)
	log.Printf("====================================")
	
	engine.Start()
	
	// Keep main goroutine alive
	select {}
}