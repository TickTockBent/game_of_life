package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
)

// Router handles all HTTP communication and message queuing
type Router struct {
	controllerEndpoint string
	
	// Separate queues for different message types
	registrationQueue chan *QueuedRequest
	stateUpdateQueue  chan *QueuedRequest
	webReadQueue      chan *QueuedRequest
	haloQueue         chan *QueuedRequest
	
	// Batching for state updates
	stateBatchQueue   chan *StateUpdateBatch
	currentBatch      []*StateUpdateBatch
	batchMutex        sync.Mutex
	batchTimer        *time.Timer
	
	// Queue size counters (atomic)
	regQueueSize   int64
	stateQueueSize int64
	webQueueSize   int64
	haloQueueSize  int64
	batchQueueSize int64
	
	// HTTP client for controller communication
	httpClient *http.Client
	regionID   string
}

type QueuedRequest struct {
	Method       string
	Path         string
	Body         []byte
	Response     chan *QueuedResponse
	Timestamp    time.Time
	ClientWriter http.ResponseWriter
	Original     *http.Request
}

type QueuedResponse struct {
	StatusCode int
	Headers    map[string]string
	Body       []byte
	Error      error
}

type StateUpdateBatch struct {
	Position     int       `json:"position"`
	Grid         [][]bool  `json:"grid"`
	Generation   int       `json:"generation"`
	Timestamp    time.Time `json:"timestamp"`
	ClientWriter http.ResponseWriter `json:"-"`
}

type BatchedStateUpdate struct {
	Updates []StateUpdateBatch `json:"updates"`
}

func NewRouter() *Router {
	controllerEndpoint := os.Getenv("CONTROLLER_ENDPOINT")
	if controllerEndpoint == "" {
		controllerEndpoint = "http://gameoflife-controller:8081"
	}
	
	regionID := os.Getenv("REGION_ID")
	if regionID == "" {
		regionID = "k3s-cluster"
	}

	r := &Router{
		controllerEndpoint: controllerEndpoint,
		regionID:          regionID,
		registrationQueue: make(chan *QueuedRequest, 200),  // Large buffers for bursts
		stateUpdateQueue:  make(chan *QueuedRequest, 2000),
		webReadQueue:      make(chan *QueuedRequest, 1000),
		haloQueue:         make(chan *QueuedRequest, 1000),
		stateBatchQueue:   make(chan *StateUpdateBatch, 2000), // For batching state updates
		currentBatch:      make([]*StateUpdateBatch, 0, 50),   // Pre-allocate for 50 updates
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
	
	// Start queue processors
	go r.processRegistrationQueue()
	go r.processBatchedStateUpdates() // Use batched processor instead
	go r.processWebReadQueue()
	go r.processHaloQueue()
	
	return r
}

// processRegistrationQueue handles engine registration requests
func (r *Router) processRegistrationQueue() {
	for req := range r.registrationQueue {
		atomic.AddInt64(&r.regQueueSize, -1)
		
		response := r.forwardToController(req)
		
		// Write response back to client
		if response.Error != nil {
			http.Error(req.ClientWriter, response.Error.Error(), http.StatusServiceUnavailable)
		} else {
			// Set headers first
			for key, value := range response.Headers {
				req.ClientWriter.Header().Set(key, value)
			}
			if req.ClientWriter.Header().Get("Content-Type") == "" {
				req.ClientWriter.Header().Set("Content-Type", "application/json")
			}
			// Write status code before body
			req.ClientWriter.WriteHeader(response.StatusCode)
			req.ClientWriter.Write(response.Body)
		}
	}
}

// processBatchedStateUpdates handles engine state updates with batching
func (r *Router) processBatchedStateUpdates() {
	const (
		maxBatchSize = 20              // Max updates per batch
		batchTimeout = 10 * time.Millisecond // Max time to wait for batch
	)
	
	var batch []*StateUpdateBatch
	var timer *time.Timer
	
	flushBatch := func() {
		if len(batch) == 0 {
			return
		}
		
		// Send batch to controller
		r.sendBatchToController(batch)
		
		// Clear batch
		batch = batch[:0]
		
		// Stop timer if running
		if timer != nil {
			timer.Stop()
		}
	}
	
	for update := range r.stateBatchQueue {
		atomic.AddInt64(&r.batchQueueSize, -1)
		
		// Add to current batch
		batch = append(batch, update)
		
		// If this is first item in batch, start timer
		if len(batch) == 1 {
			timer = time.AfterFunc(batchTimeout, flushBatch)
		}
		
		// If batch is full, flush immediately
		if len(batch) >= maxBatchSize {
			flushBatch()
		}
	}
}

// sendBatchToController sends a batch of state updates to the controller
func (r *Router) sendBatchToController(batch []*StateUpdateBatch) {
	if len(batch) == 0 {
		return
	}
	
	// Convert to API format (without ClientWriter)
	updates := make([]StateUpdateBatch, len(batch))
	for i, update := range batch {
		updates[i] = StateUpdateBatch{
			Position:   update.Position,
			Grid:       update.Grid,
			Generation: update.Generation,
			Timestamp:  update.Timestamp,
		}
	}
	
	batchRequest := BatchedStateUpdate{Updates: updates}
	
	// Serialize batch
	body, err := json.Marshal(batchRequest)
	if err != nil {
		log.Printf("Failed to marshal batch: %v", err)
		// Respond with error to all clients in batch
		for _, update := range batch {
			http.Error(update.ClientWriter, "Internal server error", http.StatusInternalServerError)
		}
		return
	}
	
	// Send to controller
	url := r.controllerEndpoint + "/batch-state"
	resp, err := r.httpClient.Post(url, "application/json", bytes.NewBuffer(body))
	
	if err != nil {
		log.Printf("Failed to send batch to controller: %v", err)
		// Respond with error to all clients in batch
		for _, update := range batch {
			http.Error(update.ClientWriter, "Controller unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	defer resp.Body.Close()
	
	// Respond to all clients in batch
	for _, update := range batch {
		update.ClientWriter.Header().Set("Content-Type", "application/json")
		update.ClientWriter.WriteHeader(resp.StatusCode)
		if resp.StatusCode != http.StatusOK {
			update.ClientWriter.Write([]byte(`{"error":"Batch update failed"}`))
		} else {
			update.ClientWriter.Write([]byte(`{"status":"ok"}`))
		}
	}
	
	log.Printf("Sent batch of %d state updates to controller", len(batch))
}

// processWebReadQueue handles web interface read requests
func (r *Router) processWebReadQueue() {
	for req := range r.webReadQueue {
		atomic.AddInt64(&r.webQueueSize, -1)
		
		response := r.forwardToController(req)
		
		// Write response back to client
		if response.Error != nil {
			http.Error(req.ClientWriter, response.Error.Error(), http.StatusServiceUnavailable)
		} else {
			// Set headers first
			for key, value := range response.Headers {
				req.ClientWriter.Header().Set(key, value)
			}
			if req.ClientWriter.Header().Get("Content-Type") == "" {
				req.ClientWriter.Header().Set("Content-Type", "application/json")
			}
			// Write status code before body
			req.ClientWriter.WriteHeader(response.StatusCode)
			req.ClientWriter.Write(response.Body)
		}
	}
}

// processHaloQueue handles halo data requests
func (r *Router) processHaloQueue() {
	for req := range r.haloQueue {
		atomic.AddInt64(&r.haloQueueSize, -1)
		
		response := r.forwardToController(req)
		
		// Write response back to client
		if response.Error != nil {
			http.Error(req.ClientWriter, response.Error.Error(), http.StatusServiceUnavailable)
		} else {
			// Set headers first
			for key, value := range response.Headers {
				req.ClientWriter.Header().Set(key, value)
			}
			if req.ClientWriter.Header().Get("Content-Type") == "" {
				req.ClientWriter.Header().Set("Content-Type", "application/json")
			}
			// Write status code before body
			req.ClientWriter.WriteHeader(response.StatusCode)
			req.ClientWriter.Write(response.Body)
		}
	}
}

// forwardToController sends request to controller and returns response
func (r *Router) forwardToController(queuedReq *QueuedRequest) *QueuedResponse {
	url := r.controllerEndpoint + queuedReq.Path
	
	var httpReq *http.Request
	var err error
	
	if len(queuedReq.Body) > 0 {
		httpReq, err = http.NewRequest(queuedReq.Method, url, bytes.NewBuffer(queuedReq.Body))
	} else {
		httpReq, err = http.NewRequest(queuedReq.Method, url, nil)
	}
	
	if err != nil {
		return &QueuedResponse{Error: err}
	}
	
	// Copy headers from original request
	httpReq.Header.Set("Content-Type", "application/json")
	
	resp, err := r.httpClient.Do(httpReq)
	if err != nil {
		return &QueuedResponse{Error: err}
	}
	defer resp.Body.Close()
	
	// Read response body - always read it regardless of Content-Length
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &QueuedResponse{Error: fmt.Errorf("failed to read response body: %v", err)}
	}
	
	// Copy response headers
	headers := make(map[string]string)
	for key, values := range resp.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	
	return &QueuedResponse{
		StatusCode: resp.StatusCode,
		Headers:    headers,
		Body:       body,
	}
}

// queueRequest queues a request for processing
func (r *Router) queueRequest(queue chan *QueuedRequest, queueCounter *int64, w http.ResponseWriter, req *http.Request, path string) {
	// Read request body
	var body []byte
	if req.ContentLength != 0 {
		body, _ = io.ReadAll(req.Body)
	}
	
	queuedReq := &QueuedRequest{
		Method:       req.Method,
		Path:         path,
		Body:         body,
		Timestamp:    time.Now(),
		ClientWriter: w,
		Original:     req,
	}
	
	// Try to queue request
	select {
	case queue <- queuedReq:
		atomic.AddInt64(queueCounter, 1)
		// Request queued successfully - response will be written by processor
	default:
		// Queue full
		http.Error(w, "Router overloaded", http.StatusServiceUnavailable)
	}
}

// HTTP Handlers

// POST /register - Engine registration
func (r *Router) handleRegister(w http.ResponseWriter, req *http.Request) {
	r.queueRequest(r.registrationQueue, &r.regQueueSize, w, req, "/register")
}

// POST /state/{position} - Engine state updates (with batching)
func (r *Router) handleStateUpdate(w http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	positionStr := vars["position"]
	position, err := strconv.Atoi(positionStr)
	if err != nil {
		http.Error(w, "Invalid position", http.StatusBadRequest)
		return
	}
	
	// Parse the state update request
	var stateReq struct {
		Grid       [][]bool `json:"grid"`
		Generation int      `json:"generation"`
	}
	
	if err := json.NewDecoder(req.Body).Decode(&stateReq); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	
	// Create batch update
	batchUpdate := &StateUpdateBatch{
		Position:     position,
		Grid:         stateReq.Grid,
		Generation:   stateReq.Generation,
		Timestamp:    time.Now(),
		ClientWriter: w,
	}
	
	// Try to queue for batching
	select {
	case r.stateBatchQueue <- batchUpdate:
		atomic.AddInt64(&r.batchQueueSize, 1)
		// Response will be sent by batch processor
	default:
		// Queue full
		http.Error(w, "Router overloaded", http.StatusServiceUnavailable)
	}
}

// GET /halo/{position} - Halo data requests
func (r *Router) handleHaloRequest(w http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	position := vars["position"]
	path := "/halo/" + position
	r.queueRequest(r.haloQueue, &r.haloQueueSize, w, req, path)
}

// GET /generation - Generation requests
func (r *Router) handleGeneration(w http.ResponseWriter, req *http.Request) {
	r.queueRequest(r.webReadQueue, &r.webQueueSize, w, req, "/generation")
}

// GET /aggregated-state - Web interface requests
func (r *Router) handleAggregatedState(w http.ResponseWriter, req *http.Request) {
	r.queueRequest(r.webReadQueue, &r.webQueueSize, w, req, "/aggregated-state")
}

// GET /topology - Topology requests
func (r *Router) handleTopology(w http.ResponseWriter, req *http.Request) {
	r.queueRequest(r.webReadQueue, &r.webQueueSize, w, req, "/topology")
}

// POST /api/click - Web interface click handling
func (r *Router) handleClick(w http.ResponseWriter, req *http.Request) {
	r.queueRequest(r.webReadQueue, &r.webQueueSize, w, req, "/api/click")
}

// POST /api/randomize - Web interface randomize all
func (r *Router) handleRandomize(w http.ResponseWriter, req *http.Request) {
	r.queueRequest(r.webReadQueue, &r.webQueueSize, w, req, "/api/randomize")
}

// GET /metrics - Router metrics (not forwarded to controller)
func (r *Router) handleMetrics(w http.ResponseWriter, req *http.Request) {
	metrics := map[string]interface{}{
		"timestamp":       time.Now().Unix(),
		"regionId":        r.regionID,
		"regQueueSize":    atomic.LoadInt64(&r.regQueueSize),
		"batchQueueSize":  atomic.LoadInt64(&r.batchQueueSize), // Batched state updates
		"webQueueSize":    atomic.LoadInt64(&r.webQueueSize),
		"haloQueueSize":   atomic.LoadInt64(&r.haloQueueSize),
		"totalQueueSize":  atomic.LoadInt64(&r.regQueueSize) +
		                  atomic.LoadInt64(&r.batchQueueSize) +
		                  atomic.LoadInt64(&r.webQueueSize) +
		                  atomic.LoadInt64(&r.haloQueueSize),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

// GET /health - Router health check
func (r *Router) handleHealth(w http.ResponseWriter, req *http.Request) {
	health := map[string]interface{}{
		"status":   "healthy",
		"regionId": r.regionID,
		"router":   "active",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}


func main() {
	router := NewRouter()

	r := mux.NewRouter()

	// Engine endpoints
	r.HandleFunc("/register", router.handleRegister).Methods("POST")
	r.HandleFunc("/state/{position}", router.handleStateUpdate).Methods("POST")
	r.HandleFunc("/halo/{position}", router.handleHaloRequest).Methods("GET")
	r.HandleFunc("/generation", router.handleGeneration).Methods("GET")

	// Web interface endpoints
	r.HandleFunc("/aggregated-state", router.handleAggregatedState).Methods("GET")
	r.HandleFunc("/topology", router.handleTopology).Methods("GET")
	
	// Legacy API mapping for web interface
	r.HandleFunc("/api/grid", router.handleAggregatedState).Methods("GET")
	r.HandleFunc("/api/click", router.handleClick).Methods("POST")
	r.HandleFunc("/api/randomize", router.handleRandomize).Methods("POST")

	// Router-specific endpoints
	r.HandleFunc("/metrics", router.handleMetrics).Methods("GET")
	r.HandleFunc("/health", router.handleHealth).Methods("GET")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Game of Life Router starting on port %s (Region: %s, Controller: %s)", 
		port, router.regionID, router.controllerEndpoint)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), r))
}