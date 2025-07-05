package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	
	"github.com/gorilla/mux"
)

func setupTestEngine() (*Engine, *mux.Router) {
	engine := NewEngine()
	
	router := mux.NewRouter()
	router.HandleFunc("/state", engine.handleGetState).Methods("GET")
	router.HandleFunc("/cell", engine.handleUpdateCell).Methods("POST")
	router.HandleFunc("/start", engine.handleStart).Methods("POST")
	router.HandleFunc("/stop", engine.handleStop).Methods("POST")
	router.HandleFunc("/step", engine.handleStep).Methods("POST")
	router.HandleFunc("/randomize", engine.handleRandomize).Methods("POST")
	router.HandleFunc("/health", engine.handleHealth).Methods("GET")
	
	return engine, router
}

func TestHealthEndpoint(t *testing.T) {
	_, router := setupTestEngine()
	
	req, _ := http.NewRequest("GET", "/health", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	
	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}
	
	var health map[string]interface{}
	err := json.NewDecoder(recorder.Body).Decode(&health)
	if err != nil {
		t.Fatal("Failed to decode health response:", err)
	}
	
	if health["status"] != "healthy" {
		t.Errorf("Expected status 'healthy', got %v", health["status"])
	}
}

func TestGetStateEndpoint(t *testing.T) {
	_, router := setupTestEngine()
	
	req, _ := http.NewRequest("GET", "/state", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	
	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}
	
	var response GridStateResponse
	err := json.NewDecoder(recorder.Body).Decode(&response)
	if err != nil {
		t.Fatal("Failed to decode state response:", err)
	}
	
	if response.Generation != 0 {
		t.Errorf("Expected initial generation 0, got %d", response.Generation)
	}
	
	if len(response.Grid) != 7 {
		t.Errorf("Expected 7x7 grid, got %d rows", len(response.Grid))
	}
	
	if len(response.Edges) != 4 {
		t.Errorf("Expected 4 edges, got %d", len(response.Edges))
	}
}

func TestUpdateCellEndpoint(t *testing.T) {
	engine, router := setupTestEngine()
	
	cellUpdate := CellUpdateRequest{
		X:     3,
		Y:     3,
		Alive: true,
	}
	
	jsonBody, _ := json.Marshal(cellUpdate)
	req, _ := http.NewRequest("POST", "/cell", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	
	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}
	
	// Verify cell was updated
	if !engine.grid.GetCell(3, 3) {
		t.Error("Cell was not updated")
	}
}

func TestStepEndpoint(t *testing.T) {
	engine, router := setupTestEngine()
	
	initialGeneration := engine.grid.GetGeneration()
	
	req, _ := http.NewRequest("POST", "/step", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	
	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}
	
	newGeneration := engine.grid.GetGeneration()
	if newGeneration != initialGeneration+1 {
		t.Errorf("Expected generation %d, got %d", initialGeneration+1, newGeneration)
	}
}

func TestStartStopEndpoints(t *testing.T) {
	engine, router := setupTestEngine()
	
	// Test start
	req, _ := http.NewRequest("POST", "/start", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	
	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200 for start, got %d", recorder.Code)
	}
	
	if !engine.running {
		t.Error("Engine should be running after start")
	}
	
	// Test start when already running
	req, _ = http.NewRequest("POST", "/start", nil)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for duplicate start, got %d", recorder.Code)
	}
	
	// Test stop
	req, _ = http.NewRequest("POST", "/stop", nil)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	
	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200 for stop, got %d", recorder.Code)
	}
	
	if engine.running {
		t.Error("Engine should not be running after stop")
	}
	
	// Test stop when already stopped
	req, _ = http.NewRequest("POST", "/stop", nil)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for duplicate stop, got %d", recorder.Code)
	}
}

func TestStepWhileRunning(t *testing.T) {
	engine, router := setupTestEngine()
	
	// Start the engine
	engine.running = true
	
	req, _ := http.NewRequest("POST", "/step", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for step while running, got %d", recorder.Code)
	}
}

func TestRandomizeEndpoint(t *testing.T) {
	engine, router := setupTestEngine()
	
	// Set generation to non-zero
	engine.grid.NextGeneration()
	engine.grid.NextGeneration()
	
	req, _ := http.NewRequest("POST", "/randomize", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	
	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}
	
	// Check that generation was reset
	if engine.grid.GetGeneration() != 0 {
		t.Error("Randomize should reset generation to 0")
	}
	
	// Check that at least some cells are alive
	state, _ := engine.grid.GetState()
	hasAliveCell := false
	for rowIndex := 0; rowIndex < len(state); rowIndex++ {
		for colIndex := 0; colIndex < len(state[rowIndex]); colIndex++ {
			if state[rowIndex][colIndex] {
				hasAliveCell = true
				break
			}
		}
	}
	
	if !hasAliveCell {
		t.Error("Randomize should create at least some alive cells")
	}
}

func TestInvalidCellUpdate(t *testing.T) {
	_, router := setupTestEngine()
	
	// Test invalid JSON
	req, _ := http.NewRequest("POST", "/cell", bytes.NewBufferString("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for invalid JSON, got %d", recorder.Code)
	}
}