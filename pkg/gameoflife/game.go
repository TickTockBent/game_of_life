package gameoflife

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

const GridSize = 7

type Cell bool

type Grid struct {
	cells    [GridSize][GridSize]Cell
	nextGen  [GridSize][GridSize]Cell
	mu       sync.RWMutex
	generation int
	// Halo region for neighbor edge data
	haloNorth []bool
	haloSouth []bool
	haloEast  []bool
	haloWest  []bool
	// Neighbor endpoints for communication
	neighbors map[string]string
	crosstalkEnabled bool
	// Boring threshold tracking
	emptyGenerations int
	boringThreshold  int
}

func NewGrid() *Grid {
	return &Grid{
		neighbors: make(map[string]string),
		crosstalkEnabled: false,
		boringThreshold: 100, // Auto-randomize after 100 empty generations
	}
}

func (g *Grid) RandomSeed(probability float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	
	for i := 0; i < GridSize; i++ {
		for j := 0; j < GridSize; j++ {
			g.cells[i][j] = Cell(rand.Float64() < probability)
		}
	}
	g.generation = 0
}

func (g *Grid) SetCell(x, y int, alive bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	
	if x >= 0 && x < GridSize && y >= 0 && y < GridSize {
		g.cells[x][y] = Cell(alive)
	}
}

func (g *Grid) GetCell(x, y int) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	
	if x >= 0 && x < GridSize && y >= 0 && y < GridSize {
		return bool(g.cells[x][y])
	}
	return false
}

func (g *Grid) countNeighbors(x, y int) int {
	count := 0
	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			if dx == 0 && dy == 0 {
				continue
			}
			nx, ny := x+dx, y+dy
			
			// Check if neighbor is within grid bounds
			if nx >= 0 && nx < GridSize && ny >= 0 && ny < GridSize {
				if g.cells[nx][ny] {
					count++
				}
			} else if g.crosstalkEnabled {
				// Check halo regions for out-of-bounds neighbors
				if g.checkHaloNeighbor(nx, ny) {
					count++
				}
			}
		}
	}
	return count
}

// checkHaloNeighbor checks if a neighbor outside the grid is alive in halo region
func (g *Grid) checkHaloNeighbor(x, y int) bool {
	// North halo (x = -1)
	if x == -1 && y >= 0 && y < GridSize && g.haloNorth != nil {
		return g.haloNorth[y]
	}
	// South halo (x = GridSize)
	if x == GridSize && y >= 0 && y < GridSize && g.haloSouth != nil {
		return g.haloSouth[y]
	}
	// West halo (y = -1)
	if y == -1 && x >= 0 && x < GridSize && g.haloWest != nil {
		return g.haloWest[x]
	}
	// East halo (y = GridSize)
	if y == GridSize && x >= 0 && x < GridSize && g.haloEast != nil {
		return g.haloEast[x]
	}
	return false
}

func (g *Grid) NextGeneration() {
	g.mu.Lock()
	defer g.mu.Unlock()
	
	// Update halo regions with neighbor edge data
	g.updateHaloRegions()
	
	for x := 0; x < GridSize; x++ {
		for y := 0; y < GridSize; y++ {
			neighbors := g.countNeighbors(x, y)
			alive := g.cells[x][y]
			
			if alive && (neighbors == 2 || neighbors == 3) {
				g.nextGen[x][y] = true
			} else if !alive && neighbors == 3 {
				g.nextGen[x][y] = true
			} else {
				g.nextGen[x][y] = false
			}
		}
	}
	
	g.cells = g.nextGen
	g.generation++
	
	// Check for boring threshold
	if g.isEmpty() {
		g.emptyGenerations++
		if g.emptyGenerations >= g.boringThreshold {
			// Note: We can't log here directly as this is a library package
			// The logging will be done in the engine when it detects the change
			g.RandomSeed(0.3) // Re-seed with 30% probability
			g.emptyGenerations = 0
		}
	} else {
		g.emptyGenerations = 0
	}
}

// isEmpty checks if the grid has no living cells
func (g *Grid) isEmpty() bool {
	for x := 0; x < GridSize; x++ {
		for y := 0; y < GridSize; y++ {
			if g.cells[x][y] {
				return false
			}
		}
	}
	return true
}

func (g *Grid) GetState() ([][GridSize]bool, int) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	
	state := make([][GridSize]bool, GridSize)
	for i := 0; i < GridSize; i++ {
		for j := 0; j < GridSize; j++ {
			state[i][j] = bool(g.cells[i][j])
		}
	}
	return state, g.generation
}

func (g *Grid) GetGeneration() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.generation
}

// GetEmptyGenerations returns the count of consecutive empty generations
func (g *Grid) GetEmptyGenerations() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.emptyGenerations
}

func (g *Grid) GetEdgeCells() map[string][]bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	
	edges := make(map[string][]bool)
	
	north := make([]bool, GridSize)
	south := make([]bool, GridSize)
	east := make([]bool, GridSize)
	west := make([]bool, GridSize)
	
	for i := 0; i < GridSize; i++ {
		north[i] = bool(g.cells[0][i])
		south[i] = bool(g.cells[GridSize-1][i])
		west[i] = bool(g.cells[i][0])
		east[i] = bool(g.cells[i][GridSize-1])
	}
	
	edges["north"] = north
	edges["south"] = south
	edges["east"] = east
	edges["west"] = west
	
	return edges
}

// SetNeighbors configures the neighbor endpoints for crosstalk
func (g *Grid) SetNeighbors(neighbors map[string]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	
	g.neighbors = neighbors
	g.crosstalkEnabled = len(neighbors) > 0
}

// fetchNeighborEdge retrieves edge data from a neighbor node
func (g *Grid) fetchNeighborEdge(direction, endpoint string) []bool {
	client := &http.Client{Timeout: 20 * time.Millisecond} // Very aggressive timeout for edge sync
	resp, err := client.Get(endpoint + "/edges/" + direction)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil
	}
	
	var edgeData []bool
	if err := json.NewDecoder(resp.Body).Decode(&edgeData); err != nil {
		return nil
	}
	
	return edgeData
}

// updateHaloRegions fetches edge data from all neighbors (non-blocking)
func (g *Grid) updateHaloRegions() {
	if !g.crosstalkEnabled {
		return
	}
	
	// Use shorter timeout and don't block the simulation if neighbors are slow
	type haloUpdate struct {
		direction string
		data      []bool
	}
	
	resultChan := make(chan haloUpdate, 4)
	activeRequests := 0
	
	// Start async requests for each neighbor
	if endpoint, exists := g.neighbors["north"]; exists {
		activeRequests++
		go func() {
			data := g.fetchNeighborEdge("south", endpoint)
			resultChan <- haloUpdate{"north", data}
		}()
	}
	if endpoint, exists := g.neighbors["south"]; exists {
		activeRequests++
		go func() {
			data := g.fetchNeighborEdge("north", endpoint)
			resultChan <- haloUpdate{"south", data}
		}()
	}
	if endpoint, exists := g.neighbors["east"]; exists {
		activeRequests++
		go func() {
			data := g.fetchNeighborEdge("west", endpoint)
			resultChan <- haloUpdate{"east", data}
		}()
	}
	if endpoint, exists := g.neighbors["west"]; exists {
		activeRequests++
		go func() {
			data := g.fetchNeighborEdge("east", endpoint)
			resultChan <- haloUpdate{"west", data}
		}()
	}
	
	// Collect results with a timeout to avoid blocking
	timeout := time.NewTimer(30 * time.Millisecond) // Very short timeout
	defer timeout.Stop()
	
	for i := 0; i < activeRequests; i++ {
		select {
		case update := <-resultChan:
			// Apply successful updates
			switch update.direction {
			case "north":
				g.haloNorth = update.data
			case "south":
				g.haloSouth = update.data
			case "east":
				g.haloEast = update.data
			case "west":
				g.haloWest = update.data
			}
		case <-timeout.C:
			// Don't wait forever - just use old halo data
			return
		}
	}
}