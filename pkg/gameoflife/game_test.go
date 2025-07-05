package gameoflife

import (
	"testing"
)

func TestNewGrid(t *testing.T) {
	grid := NewGrid()
	if grid == nil {
		t.Fatal("NewGrid() returned nil")
	}
	
	// Verify empty grid
	state, generation := grid.GetState()
	if generation != 0 {
		t.Errorf("Expected generation 0, got %d", generation)
	}
	
	for rowIndex := 0; rowIndex < GridSize; rowIndex++ {
		for colIndex := 0; colIndex < GridSize; colIndex++ {
			if state[rowIndex][colIndex] {
				t.Errorf("Expected cell [%d,%d] to be dead, but it was alive", rowIndex, colIndex)
			}
		}
	}
}

func TestSetAndGetCell(t *testing.T) {
	grid := NewGrid()
	
	// Test setting cells
	testCases := []struct {
		x, y  int
		alive bool
		desc  string
	}{
		{0, 0, true, "top-left corner"},
		{6, 6, true, "bottom-right corner"},
		{3, 3, true, "center"},
		{3, 3, false, "center turned off"},
	}
	
	for _, tc := range testCases {
		grid.SetCell(tc.x, tc.y, tc.alive)
		got := grid.GetCell(tc.x, tc.y)
		if got != tc.alive {
			t.Errorf("SetCell(%d, %d, %v) - %s: expected %v, got %v", 
				tc.x, tc.y, tc.alive, tc.desc, tc.alive, got)
		}
	}
	
	// Test out of bounds
	grid.SetCell(-1, 0, true)
	grid.SetCell(0, -1, true)
	grid.SetCell(GridSize, 0, true)
	grid.SetCell(0, GridSize, true)
	
	// Out of bounds should return false
	if grid.GetCell(-1, 0) {
		t.Error("GetCell(-1, 0) should return false for out of bounds")
	}
	if grid.GetCell(GridSize, 0) {
		t.Error("GetCell(GridSize, 0) should return false for out of bounds")
	}
}

func TestGameOfLifeRules(t *testing.T) {
	testCases := []struct {
		name     string
		setup    func(*Grid)
		expected []struct{ x, y int; alive bool }
	}{
		{
			name: "Block pattern (still life)",
			setup: func(g *Grid) {
				g.SetCell(2, 2, true)
				g.SetCell(2, 3, true)
				g.SetCell(3, 2, true)
				g.SetCell(3, 3, true)
			},
			expected: []struct{ x, y int; alive bool }{
				{2, 2, true}, {2, 3, true}, {3, 2, true}, {3, 3, true},
			},
		},
		{
			name: "Blinker pattern (oscillator)",
			setup: func(g *Grid) {
				g.SetCell(3, 2, true)
				g.SetCell(3, 3, true)
				g.SetCell(3, 4, true)
			},
			expected: []struct{ x, y int; alive bool }{
				{2, 3, true}, {3, 3, true}, {4, 3, true},
				{3, 2, false}, {3, 4, false},
			},
		},
		{
			name: "Single cell dies (underpopulation)",
			setup: func(g *Grid) {
				g.SetCell(3, 3, true)
			},
			expected: []struct{ x, y int; alive bool }{
				{3, 3, false},
			},
		},
		{
			name: "Cell with 3 neighbors births",
			setup: func(g *Grid) {
				g.SetCell(2, 2, true)
				g.SetCell(2, 3, true)
				g.SetCell(3, 2, true)
			},
			expected: []struct{ x, y int; alive bool }{
				{2, 2, true}, {2, 3, true}, {3, 2, true}, {3, 3, true},
			},
		},
		{
			name: "Overpopulation kills cells",
			setup: func(g *Grid) {
				// Create a cross pattern
				g.SetCell(3, 2, true)
				g.SetCell(3, 3, true)
				g.SetCell(3, 4, true)
				g.SetCell(2, 3, true)
				g.SetCell(4, 3, true)
			},
			expected: []struct{ x, y int; alive bool }{
				{3, 3, false}, // Center dies from overpopulation
			},
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			grid := NewGrid()
			tc.setup(grid)
			
			grid.NextGeneration()
			
			for _, exp := range tc.expected {
				got := grid.GetCell(exp.x, exp.y)
				if got != exp.alive {
					t.Errorf("Cell [%d,%d] expected %v, got %v", 
						exp.x, exp.y, exp.alive, got)
				}
			}
		})
	}
}

func TestGeneration(t *testing.T) {
	grid := NewGrid()
	
	initialGen := grid.GetGeneration()
	if initialGen != 0 {
		t.Errorf("Expected initial generation 0, got %d", initialGen)
	}
	
	// Advance multiple generations
	for expectedGen := 1; expectedGen <= 5; expectedGen++ {
		grid.NextGeneration()
		currentGen := grid.GetGeneration()
		if currentGen != expectedGen {
			t.Errorf("Expected generation %d, got %d", expectedGen, currentGen)
		}
	}
}

func TestGetEdgeCells(t *testing.T) {
	grid := NewGrid()
	
	// Set specific edge cells
	grid.SetCell(0, 3, true) // North edge middle
	grid.SetCell(6, 3, true) // South edge middle
	grid.SetCell(3, 0, true) // West edge middle
	grid.SetCell(3, 6, true) // East edge middle
	
	edges := grid.GetEdgeCells()
	
	// Check north edge
	if !edges["north"][3] {
		t.Error("Expected north edge position 3 to be true")
	}
	
	// Check south edge
	if !edges["south"][3] {
		t.Error("Expected south edge position 3 to be true")
	}
	
	// Check west edge
	if !edges["west"][3] {
		t.Error("Expected west edge position 3 to be true")
	}
	
	// Check east edge
	if !edges["east"][3] {
		t.Error("Expected east edge position 3 to be true")
	}
	
	// Verify edge arrays have correct length
	for edgeName, edgeArray := range edges {
		if len(edgeArray) != GridSize {
			t.Errorf("Edge %s has wrong length: expected %d, got %d", 
				edgeName, GridSize, len(edgeArray))
		}
	}
}

func TestRandomSeed(t *testing.T) {
	grid := NewGrid()
	
	// Seed with 0 probability - should all be dead
	grid.RandomSeed(0.0)
	state, _ := grid.GetState()
	for rowIndex := 0; rowIndex < GridSize; rowIndex++ {
		for colIndex := 0; colIndex < GridSize; colIndex++ {
			if state[rowIndex][colIndex] {
				t.Error("With 0 probability, all cells should be dead")
			}
		}
	}
	
	// Seed with 1.0 probability - should all be alive
	grid.RandomSeed(1.0)
	state, _ = grid.GetState()
	for rowIndex := 0; rowIndex < GridSize; rowIndex++ {
		for colIndex := 0; colIndex < GridSize; colIndex++ {
			if !state[rowIndex][colIndex] {
				t.Error("With 1.0 probability, all cells should be alive")
			}
		}
	}
	
	// Test generation reset
	grid.NextGeneration()
	grid.NextGeneration()
	grid.RandomSeed(0.5)
	if grid.GetGeneration() != 0 {
		t.Error("RandomSeed should reset generation to 0")
	}
}

func TestConcurrency(t *testing.T) {
	grid := NewGrid()
	
	// Run multiple goroutines that read and write
	done := make(chan bool)
	
	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			grid.SetCell(i%GridSize, i%GridSize, true)
			grid.NextGeneration()
		}
		done <- true
	}()
	
	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			_, _ = grid.GetState()
			_ = grid.GetEdgeCells()
			_ = grid.GetGeneration()
		}
		done <- true
	}()
	
	// Wait for both to complete
	<-done
	<-done
	
	// If we get here without deadlock or panic, concurrency is working
	t.Log("Concurrent access test passed")
}