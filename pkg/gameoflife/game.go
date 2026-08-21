package gameoflife

import (
	"math/rand"
)

const GridSize = 7

type Cell bool

type Grid struct {
	Cells      [GridSize][GridSize]Cell
	NextGen    [GridSize][GridSize]Cell
	Generation int
	// Halo region for neighbor edge data
	haloNorth []bool
	haloSouth []bool
	haloEast  []bool
	haloWest  []bool
	// Full 9x9 halo (our 7x7 in the centre, neighbours' edge cells around it,
	// including diagonal corners). When set it takes precedence over the edges.
	halo    [GridSize + 2][GridSize + 2]bool
	haloSet bool
	// Pre-allocated edge data structures to avoid allocations
	edgeData map[string][]bool
	// Pre-allocated state array for GetState() to avoid allocations
	stateArray [][GridSize]bool
	// Neighbor endpoints for communication
	neighbors        map[string]string
	crosstalkEnabled bool
	// Staleness detection
	emptyGenerations int
	boringThreshold  int

	// Oscillation detection - store last few states
	stateHistory         [][GridSize][GridSize]Cell
	historyIndex         int
	historySize          int
	stableGenerations    int
	oscillationThreshold int

	// Low activity detection
	lastChangedCells       int
	lowActivityThreshold   int
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
		neighbors:        make(map[string]string),
		crosstalkEnabled: false,
		boringThreshold:  40, // An empty section re-seeds after ~10s; nothing to watch otherwise
		edgeData:         edgeData,
		// Pre-allocate halo regions too
		haloNorth: make([]bool, GridSize),
		haloSouth: make([]bool, GridSize),
		haloEast:  make([]bool, GridSize),
		haloWest:  make([]bool, GridSize),
		// Pre-allocate state array
		stateArray: make([][GridSize]bool, GridSize),
		// Staleness detection
		stateHistory:           stateHistory,
		historySize:            historySize,
		oscillationThreshold:   120, // ~30s at 4 gen/s of a still life / oscillator
		lowActivityThreshold:   3,   // Less than 3 cells changing = low activity
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

// Patterns that stay interesting on a 7x7 section and tend to spill into
// neighbours (which is the whole point of a distributed grid).
var seedPatterns = [][]string{
	{".XX", "XX.", ".X."},                // R-pentomino: chaotic for ~1100 generations
	{".X.", "..X", "XXX"},                // glider
	{".X.....", "...X...", "XX..XXX"},    // acorn (7 wide, fits exactly)
	{".X..X", "X....", "X...X", "XXXX."}, // lightweight spaceship
	{"XXX", "X..", ".X."},                // a small "B-heptomino"-ish seed
}

// SeedPattern clears the section and drops a random pattern at a random
// offset and orientation. Falls back to 30% noise one time in five so the
// grid doesn't look too curated.
func (g *Grid) SeedPattern() {
	if rand.Intn(5) == 0 {
		g.RandomSeed(0.3)
		return
	}
	shape := patternCells(seedPatterns[rand.Intn(len(seedPatterns))])
	for turns := rand.Intn(4); turns > 0; turns-- {
		shape = rotateCells(shape)
	}
	if rand.Intn(2) == 1 {
		shape = flipCells(shape)
	}
	shape, height, width := normaliseCells(shape)
	offR := rand.Intn(GridSize - height + 1)
	offC := rand.Intn(GridSize - width + 1)
	g.Cells = [GridSize][GridSize]Cell{}
	for _, cell := range shape {
		g.Cells[cell[0]+offR][cell[1]+offC] = true
	}
	g.Generation = 0
}

func patternCells(pattern []string) [][2]int {
	cells := make([][2]int, 0, 16)
	for r, row := range pattern {
		for c := 0; c < len(row); c++ {
			if row[c] == 'X' {
				cells = append(cells, [2]int{r, c})
			}
		}
	}
	return cells
}

func rotateCells(cells [][2]int) [][2]int { // 90 degrees: (r,c) -> (c,-r)
	out := make([][2]int, len(cells))
	for i, cell := range cells {
		out[i] = [2]int{cell[1], -cell[0]}
	}
	return out
}

func flipCells(cells [][2]int) [][2]int {
	out := make([][2]int, len(cells))
	for i, cell := range cells {
		out[i] = [2]int{cell[0], -cell[1]}
	}
	return out
}

// normaliseCells shifts a shape so its top-left is (0,0) and returns its size.
func normaliseCells(cells [][2]int) ([][2]int, int, int) {
	minR, minC := cells[0][0], cells[0][1]
	for _, cell := range cells {
		if cell[0] < minR {
			minR = cell[0]
		}
		if cell[1] < minC {
			minC = cell[1]
		}
	}
	maxR, maxC := 0, 0
	out := make([][2]int, len(cells))
	for i, cell := range cells {
		out[i] = [2]int{cell[0] - minR, cell[1] - minC}
		if out[i][0] > maxR {
			maxR = out[i][0]
		}
		if out[i][1] > maxC {
			maxC = out[i][1]
		}
	}
	return out, maxR + 1, maxC + 1
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

// checkHaloNeighbor reports whether a neighbour just outside the grid is alive.
func (g *Grid) checkHaloNeighbor(x, y int) bool {
	if g.haloSet {
		return g.halo[x+1][y+1]
	}
	switch {
	case x == -1 && y >= 0 && y < GridSize && g.haloNorth != nil:
		return g.haloNorth[y]
	case x == GridSize && y >= 0 && y < GridSize && g.haloSouth != nil:
		return g.haloSouth[y]
	case y == -1 && x >= 0 && x < GridSize && g.haloWest != nil:
		return g.haloWest[x]
	case y == GridSize && x >= 0 && x < GridSize && g.haloEast != nil:
		return g.haloEast[x]
	}
	return false
}

// SetHalo installs the full 9x9 neighbourhood for the next generation.
func (g *Grid) SetHalo(halo [GridSize + 2][GridSize + 2]bool) {
	g.halo = halo
	g.haloSet = true
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
			g.SeedPattern() // Auto-reseed when boring
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
		g.SeedPattern()
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
	if g.lowActivityGenerations >= 160 { // ~40s at 4 gen/s of barely anything changing
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
		"emptyGenerations":       g.emptyGenerations,
		"lowActivityGenerations": g.lowActivityGenerations,
		"stableGenerations":      g.stableGenerations,
		"lastChangedCells":       g.lastChangedCells,
		"generation":             g.Generation,
	}
}
