package grid

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type Direction string

const (
	North Direction = "north"
	South Direction = "south"
	East  Direction = "east"
	West  Direction = "west"
)

type Neighbor struct {
	Direction Direction
	Position  int
	Endpoint  string
}

type DistributedGrid struct {
	position         int
	neighbors        map[Direction]*Neighbor
	controllerURL    string
	selfEndpoint     string
	neighborEdges    map[Direction][]bool
	mu               sync.RWMutex
	httpClient       *http.Client
}

func NewDistributedGrid(controllerURL, selfEndpoint string) *DistributedGrid {
	return &DistributedGrid{
		position:      -1,
		neighbors:     make(map[Direction]*Neighbor),
		controllerURL: controllerURL,
		selfEndpoint:  selfEndpoint,
		neighborEdges: make(map[Direction][]bool),
		httpClient: &http.Client{
			Timeout: 2 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

type RegisterRequest struct {
	PodID    string `json:"podId"`
	Endpoint string `json:"endpoint"`
}

type RegisterResponse struct {
	Position  int            `json:"position"`
	Neighbors map[string]int `json:"neighbors"`
}

type NodeInfo struct {
	PodID    string `json:"podId"`
	Endpoint string `json:"endpoint"`
}

// Register with the controller and get assigned position
func (dg *DistributedGrid) Register(podID string) error {
	reqBody := RegisterRequest{
		PodID:    podID,
		Endpoint: dg.selfEndpoint,
	}
	
	jsonBody, _ := json.Marshal(reqBody)
	resp, err := dg.httpClient.Post(
		dg.controllerURL+"/register",
		"application/json",
		bytes.NewBuffer(jsonBody),
	)
	if err != nil {
		return fmt.Errorf("failed to register: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("registration failed: %s", string(body))
	}
	
	var regResp RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}
	
	dg.mu.Lock()
	dg.position = regResp.Position
	dg.mu.Unlock()
	
	// Discover neighbors
	return dg.discoverNeighbors(regResp.Neighbors)
}

// Discover and cache neighbor endpoints
func (dg *DistributedGrid) discoverNeighbors(neighborPositions map[string]int) error {
	dg.mu.Lock()
	defer dg.mu.Unlock()
	
	for dirStr, pos := range neighborPositions {
		direction := Direction(dirStr)
		
		// Get neighbor info from controller
		resp, err := dg.httpClient.Get(
			fmt.Sprintf("%s/node/%d", dg.controllerURL, pos),
		)
		if err != nil {
			continue // Neighbor might not be registered yet
		}
		defer resp.Body.Close()
		
		if resp.StatusCode == http.StatusOK {
			var nodeInfo NodeInfo
			if err := json.NewDecoder(resp.Body).Decode(&nodeInfo); err == nil {
				dg.neighbors[direction] = &Neighbor{
					Direction: direction,
					Position:  pos,
					Endpoint:  nodeInfo.Endpoint,
				}
			}
		}
	}
	
	return nil
}

// FetchNeighborEdges pulls edge data from all neighbors
func (dg *DistributedGrid) FetchNeighborEdges() error {
	dg.mu.RLock()
	neighbors := make(map[Direction]*Neighbor)
	for k, v := range dg.neighbors {
		neighbors[k] = v
	}
	dg.mu.RUnlock()
	
	edges := make(map[Direction][]bool)
	var wg sync.WaitGroup
	var mu sync.Mutex
	
	for direction, neighbor := range neighbors {
		wg.Add(1)
		go func(dir Direction, n *Neighbor) {
			defer wg.Done()
			
			// Map our direction to neighbor's opposite direction
			oppositeDir := getOppositeDirection(dir)
			
			resp, err := dg.httpClient.Get(
				fmt.Sprintf("%s/edges/%s", n.Endpoint, oppositeDir),
			)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			
			if resp.StatusCode == http.StatusOK {
				var edgeData []bool
				if err := json.NewDecoder(resp.Body).Decode(&edgeData); err == nil {
					mu.Lock()
					edges[dir] = edgeData
					mu.Unlock()
				}
			}
		}(direction, neighbor)
	}
	
	wg.Wait()
	
	dg.mu.Lock()
	dg.neighborEdges = edges
	dg.mu.Unlock()
	
	return nil
}

// GetNeighborEdge returns the cached edge data for a direction
func (dg *DistributedGrid) GetNeighborEdge(direction Direction) []bool {
	dg.mu.RLock()
	defer dg.mu.RUnlock()
	
	return dg.neighborEdges[direction]
}

// GetPosition returns the assigned grid position
func (dg *DistributedGrid) GetPosition() int {
	dg.mu.RLock()
	defer dg.mu.RUnlock()
	return dg.position
}

// RefreshNeighbors updates neighbor information from controller
func (dg *DistributedGrid) RefreshNeighbors() error {
	resp, err := dg.httpClient.Get(
		fmt.Sprintf("%s/neighbors/%d", dg.controllerURL, dg.position),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	var neighbors map[string]*NodeInfo
	if err := json.NewDecoder(resp.Body).Decode(&neighbors); err != nil {
		return err
	}
	
	dg.mu.Lock()
	defer dg.mu.Unlock()
	
	// Update neighbor cache
	dg.neighbors = make(map[Direction]*Neighbor)
	for dirStr, nodeInfo := range neighbors {
		direction := Direction(dirStr)
		// Get position from controller's neighbor data
		// This would need the position in NodeInfo or a separate lookup
		dg.neighbors[direction] = &Neighbor{
			Direction: direction,
			Endpoint:  nodeInfo.Endpoint,
		}
	}
	
	return nil
}

func getOppositeDirection(dir Direction) Direction {
	switch dir {
	case North:
		return South
	case South:
		return North
	case East:
		return West
	case West:
		return East
	default:
		return dir
	}
}