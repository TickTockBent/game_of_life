package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

func setupTestEngine() (*Engine, *mux.Router) {
	engine := NewEngine()

	router := mux.NewRouter()
	router.HandleFunc("/health", engine.handleHealth).Methods("GET")
	router.HandleFunc("/step", engine.handleStep).Methods("POST")
	router.HandleFunc("/randomize", engine.handleRandomize).Methods("POST")

	return engine, router
}

func TestHealthEndpoint(t *testing.T) {
	engine, router := setupTestEngine()

	req, _ := http.NewRequest("GET", "/health", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", recorder.Code)
	}

	var health map[string]interface{}
	if err := json.NewDecoder(recorder.Body).Decode(&health); err != nil {
		t.Fatal("Failed to decode health response:", err)
	}

	if health["status"] != "healthy" {
		t.Errorf("Expected status 'healthy', got %v", health["status"])
	}
	if health["nodeId"] != engine.nodeID {
		t.Errorf("Expected nodeId '%s', got %v", engine.nodeID, health["nodeId"])
	}
	if health["registered"] != false {
		t.Error("Expected registered=false for new engine")
	}
	if health["position"].(float64) != -1 {
		t.Errorf("Expected position -1 for unregistered engine, got %v", health["position"])
	}
}

func TestStepWhenUnregistered(t *testing.T) {
	_, router := setupTestEngine()

	req, _ := http.NewRequest("POST", "/step", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status 503 for step when unregistered, got %d", recorder.Code)
	}
}

func TestStepWhenRegistered(t *testing.T) {
	engine, router := setupTestEngine()

	// Simulate successful registration
	engine.registered = true
	engine.position = 0

	req, _ := http.NewRequest("POST", "/step", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200 for step when registered, got %d", recorder.Code)
	}

	// Verify the step signal was queued on the step channel
	select {
	case <-engine.stepChan:
		// Step signal received - expected
	default:
		t.Error("Expected step signal to be queued on stepChan")
	}
}

func TestStepChannelFull(t *testing.T) {
	engine, router := setupTestEngine()
	engine.registered = true
	engine.position = 0

	// Fill the step channel (capacity is 10)
	for i := 0; i < 10; i++ {
		engine.stepChan <- struct{}{}
	}

	// Now step should still return 200 (drops the signal gracefully)
	req, _ := http.NewRequest("POST", "/step", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200 when step channel is full, got %d", recorder.Code)
	}
}

func TestRandomizeEndpoint(t *testing.T) {
	engine, router := setupTestEngine()

	// Advance generation to non-zero
	engine.grid.NextGeneration()
	engine.grid.NextGeneration()
	initialGen := engine.grid.GetGeneration()
	if initialGen != 2 {
		t.Fatalf("Expected generation 2 before randomize, got %d", initialGen)
	}

	req, _ := http.NewRequest("POST", "/randomize", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200 for randomize, got %d", recorder.Code)
	}

	// Randomize should reset generation to 0
	if engine.grid.GetGeneration() != 0 {
		t.Errorf("Expected generation 0 after randomize, got %d", engine.grid.GetGeneration())
	}

	// Check that at least some cells are alive
	state, _ := engine.grid.GetState()
	hasAliveCell := false
	for row := range state {
		for col := range state[row] {
			if state[row][col] {
				hasAliveCell = true
				break
			}
		}
		if hasAliveCell {
			break
		}
	}
	if !hasAliveCell {
		t.Error("Randomize should create at least some alive cells")
	}
}
