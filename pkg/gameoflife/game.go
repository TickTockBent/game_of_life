package gameoflife

import (
	"log"
	"math/rand"
)

const GridSize = 7

type Cell bool

type Grid struct {
	Cells    [GridSize][GridSize]Cell
	NextGen  [GridSize][GridSize]Cell
	Generation int
	// Halo region for neighbor edge data
	haloNorth []bool
	haloSouth []bool
	haloEast  []bool
	haloWest  []bool
	// Pre-allocated edge data structures to avoid allocations
	edgeData map[string][]bool
	// Pre-allocated state array for GetState() to avoid allocations
	stateArray [][GridSize]bool
	// Neighbor endpoints for communication
	neighbors map[string]string
	crosstalkEnabled bool
	// Staleness detection
	emptyGenerations int
	boringThreshold  int
	
	// Oscillation detection - store last few states
	stateHistory [][GridSize][GridSize]Cell
	historyIndex int
	historySize  int
	stableGenerations int
	oscillationThreshold int
	
	// Low activity detection
	lastChangedCells int
	lowActivityThreshold int
	lowActivityGenerations int
}

func NewGrid() *Grid {
	// Pre-allocate all edge data structures once to avoid allocations in hot paths
	edgeData := make(map[string][]bool)
	edgeData["north"] = make([]bool, GridSize)
	edgeData["south"] = make([]bool, GridSize)
	edgeData["east"] = make([]bool, GridSize)
	edgeData["west"] = make([]bool, GridSize)
	
	// Initialize state history for oscillation detection
	historySize := 5 // Track last 5 states to detect cycles
	stateHistory := make([][GridSize][GridSize]Cell, historySize)
	
	return &Grid{
		neighbors: make(map[string]string),
		crosstalkEnabled: false,
		boringThreshold: 1000, // Auto-randomize after 1000 empty generations
		edgeData: edgeData,
		// Pre-allocate halo regions too
		haloNorth: make([]bool, GridSize),
		haloSouth: make([]bool, GridSize),
		haloEast:  make([]bool, GridSize),
		haloWest:  make([]bool, GridSize),
		// Pre-allocate state array 
		stateArray: make([][GridSize]bool, GridSize),
		// Staleness detection
		stateHistory: stateHistory,
		historySize: historySize,
		oscillationThreshold: 200, // Detect stable patterns after 200 generations
		lowActivityThreshold: 3,   // Less than 3 cells changing = low activity
		lowActivityGenerations: 0,
	}
}

func (g *Grid) RandomSeed(probability float64) {
	for i := 0; i < GridSize; i++ {
		for j := 0; j < GridSize; j++ {
			g.Cells[i][j] = Cell(rand.Float64() < probability)
		}
	}
	g.Generation = 0
}

func (g *Grid) SetCell(x, y int, alive bool) {
	if x >= 0 && x < GridSize && y >= 0 && y < GridSize {
		g.Cells[x][y] = Cell(alive)
	}
}

func (g *Grid) GetCell(x, y int) bool {
	if x >= 0 && x < GridSize && y >= 0 && y < GridSize {
		return bool(g.Cells[x][y])
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
				if g.Cells[nx][ny] {
					count++
				}
			} else {
				// Always check halo regions for out-of-bounds neighbors
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
	// Note: This function is called from within countNeighbors which is already under mu.Lock()
	// So we don't need additional locking here - the halo data is protected by the existing lock
	
	// North halo (x = -1)
	if x == -1 && y >= 0 && y < GridSize && g.haloNorth != nil {
		result := g.haloNorth[y]
		// Log only first few calls to avoid spam
		if g.Generation < 30 {
			log.Printf("DEBUG: checkHaloNeighbor(%d,%d) - north halo[%d] = %v", x, y, y, result)
		}
		return result
	}
	// South halo (x = GridSize)
	if x == GridSize && y >= 0 && y < GridSize && g.haloSouth != nil {
		result := g.haloSouth[y]
		if g.Generation < 30 {
			log.Printf("DEBUG: checkHaloNeighbor(%d,%d) - south halo[%d] = %v", x, y, y, result)
		}
		return result
	}
	// West halo (y = -1)
	if y == -1 && x >= 0 && x < GridSize && g.haloWest != nil {
		result := g.haloWest[x]
		if g.Generation < 30 {
			log.Printf("DEBUG: checkHaloNeighbor(%d,%d) - west halo[%d] = %v", x, y, x, result)
		}
		return result
	}
	// East halo (y = GridSize)
	if y == GridSize && x >= 0 && x < GridSize && g.haloEast != nil {
		result := g.haloEast[x]
		if g.Generation < 30 {
			log.Printf("DEBUG: checkHaloNeighbor(%d,%d) - east halo[%d] = %v", x, y, x, result)
		}
		return result
	}
	if g.Generation < 30 {
		log.Printf("DEBUG: checkHaloNeighbor(%d,%d) - no halo match, returning false", x, y)
	}
	return false
}

// ComputeNextGeneration calculates the next generation but doesn't commit it yet
func (g *Grid) ComputeNextGeneration() {
	for x := 0; x < GridSize; x++ {
		for y := 0; y < GridSize; y++ {
			neighbors := g.countNeighbors(x, y)
			
			// Apply Conway's Game of Life rules
			if g.Cells[x][y] {
				g.NextGen[x][y] = neighbors == 2 || neighbors == 3
			} else {
				g.NextGen[x][y] = neighbors == 3
			}
		}
	}
}

// CommitNextGeneration commits the computed next generation
func (g *Grid) CommitNextGeneration() {
	// Count cells that changed this generation
	changedCells := g.countChangedCells()
	
	// Store current state in history for oscillation detection
	g.storeCurrentState()
	
	// Copy NextGen to Cells and increment generation
	g.Cells = g.NextGen
	g.Generation++
	
	// Update staleness tracking
	g.updateStalenessDetection(changedCells)
}

// NextGeneration keeps backward compatibility (compute + commit in one call)
func (g *Grid) NextGeneration() {
	g.ComputeNextGeneration()
	g.CommitNextGeneration()
}

// Legacy implementation for reference (now replaced by ComputeNextGeneration)
func (g *Grid) nextGenerationLegacy() {
	
	for x := 0; x < GridSize; x++ {
		for y := 0; y < GridSize; y++ {
			neighbors := g.countNeighbors(x, y)
			alive := g.Cells[x][y]
			
			if alive && (neighbors == 2 || neighbors == 3) {
				g.NextGen[x][y] = true
			} else if !alive && neighbors == 3 {
				g.NextGen[x][y] = true
			} else {
				g.NextGen[x][y] = false
			}
		}
	}
	
	g.Cells = g.NextGen
	g.Generation++
	
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
			if g.Cells[x][y] {
				return false
			}
		}
	}
	return true
}

func (g *Grid) GetState() ([][GridSize]bool, int) {
	// Reuse pre-allocated state array instead of creating new one
	for i := 0; i < GridSize; i++ {
		for j := 0; j < GridSize; j++ {
			g.stateArray[i][j] = bool(g.Cells[i][j])
		}
	}
	return g.stateArray, g.Generation
}

func (g *Grid) GetGeneration() int {
	return g.Generation
}

// GetEmptyGenerations returns the count of consecutive empty generations
func (g *Grid) GetEmptyGenerations() int {
	return g.emptyGenerations
}

func (g *Grid) GetEdgeCells() map[string][]bool {
	// Reuse pre-allocated slices instead of creating new ones
	north := g.edgeData["north"]
	south := g.edgeData["south"]
	east := g.edgeData["east"]
	west := g.edgeData["west"]
	
	// Just overwrite the existing slice contents
	for i := 0; i < GridSize; i++ {
		north[i] = bool(g.Cells[0][i])
		south[i] = bool(g.Cells[GridSize-1][i])
		west[i] = bool(g.Cells[i][0])
		east[i] = bool(g.Cells[i][GridSize-1])
	}
	
	// Return the same map each time (no new allocation)
	return g.edgeData
}

// SetNeighbors configures the neighbor endpoints for crosstalk
func (g *Grid) SetNeighbors(neighbors map[string]string) {
	g.neighbors = neighbors
	g.crosstalkEnabled = len(neighbors) > 0
}

// UpdateHaloRegion updates a specific halo region with new edge data
// This will be called during neighbor polling phase
func (g *Grid) UpdateHaloRegion(direction string, edgeData []bool) {
	switch direction {
	case "north":
		g.haloNorth = edgeData
	case "south":
		g.haloSouth = edgeData
	case "east":
		g.haloEast = edgeData
	case "west":
		g.haloWest = edgeData
	}
}

// countChangedCells counts how many cells changed from current to next generation
func (g *Grid) countChangedCells() int {
	count := 0
	for x := 0; x < GridSize; x++ {
		for y := 0; y < GridSize; y++ {
			if g.Cells[x][y] != g.NextGen[x][y] {
				count++
			}
		}
	}
	return count
}

// storeCurrentState saves current state to history buffer for oscillation detection
func (g *Grid) storeCurrentState() {
	g.stateHistory[g.historyIndex] = g.Cells
	g.historyIndex = (g.historyIndex + 1) % g.historySize
}

// updateStalenessDetection updates all staleness counters and triggers randomization if needed
func (g *Grid) updateStalenessDetection(changedCells int) {
	// Empty grid detection (existing)
	if g.isEmpty() {
		g.emptyGenerations++
		if g.emptyGenerations >= g.boringThreshold {
			g.RandomSeed(0.3) // Auto-randomize when boring
			g.resetStalenessCounters()
			return
		}
	} else {
		g.emptyGenerations = 0
	}
	
	// Low activity detection
	if changedCells <= g.lowActivityThreshold {
		g.lowActivityGenerations++
	} else {
		g.lowActivityGenerations = 0
	}
	
	// Oscillation detection
	if g.Generation > g.historySize && g.isOscillating() {
		g.stableGenerations++
	} else {
		g.stableGenerations = 0
	}
	
	// Trigger randomization if stale
	if g.isStale() {
		g.RandomSeed(0.3)
		g.resetStalenessCounters()
	}
	
	g.lastChangedCells = changedCells
}

// isOscillating checks if current state matches any previous state in history
func (g *Grid) isOscillating() bool {
	for i := 0; i < g.historySize; i++ {
		if i == g.historyIndex {
			continue // Skip current slot
		}
		if g.statesEqual(g.Cells, g.stateHistory[i]) {
			return true
		}
	}
	return false
}

// statesEqual compares two grid states for equality
func (g *Grid) statesEqual(state1, state2 [GridSize][GridSize]Cell) bool {
	for x := 0; x < GridSize; x++ {
		for y := 0; y < GridSize; y++ {
			if state1[x][y] != state2[x][y] {
				return false
			}
		}
	}
	return true
}

// isStale determines if the grid should be randomized based on multiple criteria
func (g *Grid) isStale() bool {
	// Stale if low activity for too long
	if g.lowActivityGenerations >= 300 { // 300 generations of minimal change
		return true
	}
	
	// Stale if oscillating for too long
	if g.stableGenerations >= g.oscillationThreshold {
		return true
	}
	
	return false
}

// resetStalenessCounters resets all staleness detection counters
func (g *Grid) resetStalenessCounters() {
	g.emptyGenerations = 0
	g.lowActivityGenerations = 0
	g.stableGenerations = 0
	g.lastChangedCells = 0
	g.historyIndex = 0
	// Clear state history
	for i := range g.stateHistory {
		g.stateHistory[i] = [GridSize][GridSize]Cell{}
	}
}

// GetStalenessInfo returns current staleness detection state for debugging/monitoring
func (g *Grid) GetStalenessInfo() map[string]int {
	return map[string]int{
		"emptyGenerations":      g.emptyGenerations,
		"lowActivityGenerations": g.lowActivityGenerations,
		"stableGenerations":     g.stableGenerations,
		"lastChangedCells":      g.lastChangedCells,
		"generation":           g.Generation,
	}
}