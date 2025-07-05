package gameoflife

import (
	"math/rand"
	"sync"
)

const GridSize = 7

type Cell bool

type Grid struct {
	cells    [GridSize][GridSize]Cell
	nextGen  [GridSize][GridSize]Cell
	mu       sync.RWMutex
	generation int
}

func NewGrid() *Grid {
	return &Grid{}
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
			if nx >= 0 && nx < GridSize && ny >= 0 && ny < GridSize {
				if g.cells[nx][ny] {
					count++
				}
			}
		}
	}
	return count
}

func (g *Grid) NextGeneration() {
	g.mu.Lock()
	defer g.mu.Unlock()
	
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