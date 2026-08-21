package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

type WebServer struct {
	controllerURL      string
	httpClient         *http.Client
	controllerAdminURL string
	wsUpgrader         websocket.Upgrader
}

type CellClickRequest struct {
	GlobalX int  `json:"globalX"`
	GlobalY int  `json:"globalY"`
	Alive   bool `json:"alive"`
}

func NewWebServer() *WebServer {
	// Use external controller URL for global connectivity
	controllerURL := os.Getenv("CONTROLLER_URL")
	if controllerURL == "" {
		controllerURL = "https://gameoflife-api.ticktockbent.com"
	}

	// Create reusable HTTP client with connection pooling
	adminURL := os.Getenv("CONTROLLER_ADMIN_URL")
	if adminURL == "" {
		adminURL = controllerURL
	}

	httpClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	return &WebServer{
		controllerURL:      controllerURL,
		controllerAdminURL: adminURL,
		httpClient:         httpClient,
		wsUpgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for development
			},
		},
	}
}

// Serve the main HTML page
func (w *WebServer) handleIndex(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Cache-Control", "no-cache, must-revalidate")
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

// Get aggregated grid state from controller
func (w *WebServer) handleGridState(rw http.ResponseWriter, r *http.Request) {
	// Fetch aggregated state from controller
	resp, err := w.httpClient.Get(w.controllerURL + "/aggregated-state")
	if err != nil {
		http.Error(rw, "Failed to get aggregated state", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		http.Error(rw, fmt.Sprintf("Controller returned status %d", resp.StatusCode), http.StatusServiceUnavailable)
		return
	}

	// Stream the response directly to client
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(resp.StatusCode)

	// Use io.Copy for efficient streaming
	io.Copy(rw, resp.Body)
}

// handleMetrics fetches and combines controller metrics with pod information
// handleCellClick forwards a click to the controller, which reseeds the
// engine owning that section over its WebSocket.
func (w *WebServer) handleCellClick(rw http.ResponseWriter, r *http.Request) {
	w.forward(rw, r, w.controllerURL+"/api/click")
}

// handleRandomizeAll asks the controller (admin surface) to reseed every engine.
func (w *WebServer) handleRandomizeAll(rw http.ResponseWriter, r *http.Request) {
	w.forward(rw, r, w.controllerAdminURL+"/api/randomize")
}

func (w *WebServer) forward(rw http.ResponseWriter, r *http.Request, target string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		http.Error(rw, "bad request", http.StatusBadRequest)
		return
	}
	resp, err := w.httpClient.Post(target, "application/json", bytes.NewReader(body))
	if err != nil {
		http.Error(rw, "controller unreachable", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(resp.StatusCode)
	io.Copy(rw, resp.Body)
}

func (w *WebServer) handleMetrics(rw http.ResponseWriter, r *http.Request) {
	// Fetch controller metrics
	controllerMetrics, err := w.fetchControllerMetrics()
	if err != nil {
		log.Printf("Error fetching controller metrics: %v", err)
		http.Error(rw, "Failed to fetch metrics", http.StatusInternalServerError)
		return
	}

	// Combine all metrics
	response := map[string]interface{}{
		"controller": controllerMetrics,
		"timestamp":  time.Now().Unix(),
	}

	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(response)
}

// fetchControllerMetrics gets metrics from the controller
func (w *WebServer) fetchControllerMetrics() (map[string]interface{}, error) {
	resp, err := w.httpClient.Get(w.controllerURL + "/metrics")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("controller metrics returned status %d", resp.StatusCode)
	}

	var metrics map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&metrics); err != nil {
		return nil, err
	}

	return metrics, nil
}

// handleWebSocket proxies WebSocket connections to the controller
func (w *WebServer) handleWebSocket(rw http.ResponseWriter, r *http.Request) {
	// Parse controller URL for WebSocket connection
	controllerURL, err := url.Parse(w.controllerURL)
	if err != nil {
		log.Printf("Failed to parse controller URL: %v", err)
		http.Error(rw, "Invalid controller URL", http.StatusInternalServerError)
		return
	}

	// Create WebSocket URL
	wsScheme := "ws"
	if controllerURL.Scheme == "https" {
		wsScheme = "wss"
	}
	wsURL := fmt.Sprintf("%s://%s/ws", wsScheme, controllerURL.Host)

	// Upgrade client connection to WebSocket
	clientConn, err := w.wsUpgrader.Upgrade(rw, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade client WebSocket: %v", err)
		return
	}
	defer clientConn.Close()

	// Connect to controller WebSocket
	controllerConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		log.Printf("Failed to connect to controller WebSocket: %v", err)
		clientConn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "Controller connection failed"))
		return
	}
	defer controllerConn.Close()

	log.Printf("WebSocket proxy established: client <-> web <-> controller")

	// Start bidirectional proxy
	done := make(chan struct{})

	// Proxy controller -> client
	go func() {
		defer close(done)
		for {
			messageType, data, err := controllerConn.ReadMessage()
			if err != nil {
				log.Printf("Controller WebSocket read error: %v", err)
				return
			}

			err = clientConn.WriteMessage(messageType, data)
			if err != nil {
				log.Printf("Client WebSocket write error: %v", err)
				return
			}
		}
	}()

	// Proxy client -> controller
	go func() {
		for {
			messageType, data, err := clientConn.ReadMessage()
			if err != nil {
				log.Printf("Client WebSocket read error: %v", err)
				return
			}

			err = controllerConn.WriteMessage(messageType, data)
			if err != nil {
				log.Printf("Controller WebSocket write error: %v", err)
				return
			}
		}
	}()

	// Wait for connection to close
	<-done
}

func main() {
	webServer := NewWebServer()

	r := mux.NewRouter()

	// Theme previews (design candidates for the UI rebuild)
	// While the UI is being iterated, never let browsers cache these.
	noCache := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			rw.Header().Set("Cache-Control", "no-cache, must-revalidate")
			next.ServeHTTP(rw, r)
		})
	}
	r.PathPrefix("/themes/").Handler(noCache(http.StripPrefix("/themes/",
		http.FileServer(http.Dir("static/public/themes/")))))

	// Serve static files
	r.PathPrefix("/static/").Handler(noCache(http.StripPrefix("/static/",
		http.FileServer(http.Dir("static/public/")))))

	// API endpoints
	r.HandleFunc("/api/topology", webServer.handleTopology).Methods("GET")
	r.HandleFunc("/api/grid", webServer.handleGridState).Methods("GET")
	r.HandleFunc("/api/click", webServer.handleCellClick).Methods("POST")
	r.HandleFunc("/api/randomize", webServer.handleRandomizeAll).Methods("POST")
	r.HandleFunc("/api/metrics", webServer.handleMetrics).Methods("GET")

	// WebSocket endpoint for real-time updates
	r.HandleFunc("/ws", webServer.handleWebSocket)

	// Main page
	r.HandleFunc("/stats.html", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", "no-cache, must-revalidate")
		http.ServeFile(rw, r, "static/public/stats.html")
	}).Methods("GET")
	r.PathPrefix("/classic/").Handler(noCache(http.StripPrefix("/classic/",
		http.FileServer(http.Dir("static/public/classic/")))))
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
