package controller

import (
	"fmt"
	"sync"
)

const RegionSize = 10 // 10x10 grid of nodes

type Position struct {
	Row int `json:"row"`
	Col int `json:"col"`
}

type NodeInfo struct {
	PodID    string   `json:"podId"`
	Endpoint string   `json:"endpoint"`
	Position Position `json:"position"`
}

type Topology struct {
	nodes map[int]*NodeInfo // position -> node info
	readyNodes map[int]bool  // position -> ready status for barrier sync
	mu    sync.RWMutex
}

func NewTopology() *Topology {
	return &Topology{
		nodes: make(map[int]*NodeInfo),
		readyNodes: make(map[int]bool),
	}
}

// PositionToIndex converts row,col to linear position (0-99)
func PositionToIndex(row, col int) int {
	return row*RegionSize + col
}

// IndexToPosition converts linear position to row,col
func IndexToPosition(index int) Position {
	return Position{
		Row: index / RegionSize,
		Col: index % RegionSize,
	}
}

// GetNeighborPositions returns the positions of all neighbors
func GetNeighborPositions(position int) map[string]int {
	pos := IndexToPosition(position)
	neighbors := make(map[string]int)
	
	// North
	if pos.Row > 0 {
		neighbors["north"] = PositionToIndex(pos.Row-1, pos.Col)
	}
	
	// South
	if pos.Row < RegionSize-1 {
		neighbors["south"] = PositionToIndex(pos.Row+1, pos.Col)
	}
	
	// West
	if pos.Col > 0 {
		neighbors["west"] = PositionToIndex(pos.Row, pos.Col-1)
	}
	
	// East
	if pos.Col < RegionSize-1 {
		neighbors["east"] = PositionToIndex(pos.Row, pos.Col+1)
	}
	
	return neighbors
}

// RegisterNode assigns a position to a new node, allowing re-registration
func (t *Topology) RegisterNode(podID, endpoint string) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	// Check if this pod is already registered (re-registration)
	for position, node := range t.nodes {
		if node.PodID == podID {
			// Update endpoint in case it changed
			node.Endpoint = endpoint
			return position, nil
		}
	}
	
	// Find first available position for new nodes
	for position := 0; position < RegionSize*RegionSize; position++ {
		if _, exists := t.nodes[position]; !exists {
			pos := IndexToPosition(position)
			t.nodes[position] = &NodeInfo{
				PodID:    podID,
				Endpoint: endpoint,
				Position: pos,
			}
			return position, nil
		}
	}
	
	return -1, fmt.Errorf("no available positions in region")
}

// UnregisterNode removes a node from the topology
func (t *Topology) UnregisterNode(position int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	delete(t.nodes, position)
}

// GetNode returns info for a specific position
func (t *Topology) GetNode(position int) (*NodeInfo, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	node, exists := t.nodes[position]
	return node, exists
}

// GetAllNodes returns the entire topology
func (t *Topology) GetAllNodes() map[int]*NodeInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	// Return a copy to prevent external modifications
	result := make(map[int]*NodeInfo)
	for k, v := range t.nodes {
		result[k] = v
	}
	return result
}

// GetNeighbors returns the node info for all neighbors of a position
func (t *Topology) GetNeighbors(position int) map[string]*NodeInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	neighbors := make(map[string]*NodeInfo)
	neighborPositions := GetNeighborPositions(position)
	
	for direction, pos := range neighborPositions {
		if node, exists := t.nodes[pos]; exists {
			neighbors[direction] = node
		}
	}
	
	return neighbors
}

// MarkNodeReady marks a node as ready for the next step
func (t *Topology) MarkNodeReady(position int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	// Only mark ready if node exists
	if _, exists := t.nodes[position]; exists {
		t.readyNodes[position] = true
	}
}

// AreAllNodesReady checks if all registered nodes are ready
func (t *Topology) AreAllNodesReady() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	for position := range t.nodes {
		if !t.readyNodes[position] {
			return false
		}
	}
	return true
}

// ResetReadyStatus clears all ready flags for the next cycle
func (t *Topology) ResetReadyStatus() {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	for position := range t.readyNodes {
		t.readyNodes[position] = false
	}
}

// GetReadyCount returns the number of ready nodes
func (t *Topology) GetReadyCount() (int, int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	readyCount := 0
	for _, ready := range t.readyNodes {
		if ready {
			readyCount++
		}
	}
	return readyCount, len(t.nodes)
}