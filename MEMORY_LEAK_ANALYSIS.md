# Memory Leak Analysis - Game of Life Engine Pods

## Summary
Engine pods are consuming 100+ MB of memory within 2-3 minutes for a game state that should only require a few hundred bits. This document outlines the identified memory leak sources and proposed fixes.

## Critical Issues in Engine Pods

### 1. Multiple Barrier Sync Loop Goroutines
**Location**: `cmd/engine/main.go:175` - `startBarrierSyncLoop()`

**Problem**: 
- The function doesn't check if a sync loop is already running
- Gets called multiple times:
  - Initial registration (line 514)
  - Re-registration after health check failure (line 170)
  - Manual `/start` endpoint (line 317)
- Each call spawns a new goroutine polling every 100ms

**Impact**: Exponential goroutine growth leading to memory exhaustion

### 2. HTTP Response Body Accumulation
**Location**: `cmd/engine/main.go:253-262` - `pollNeighborEdges()`

**Problem**:
```go
defer resp.Body.Close()  // Inside a loop!
```
- Response bodies are closed in a deferred function inside a loop
- Deferred functions accumulate until the parent function returns
- With 4 neighbors polled every 100ms = 40 response bodies/second held in memory

**Impact**: Memory accumulation from unclosed response bodies

### 3. Inadequate Goroutine Cleanup
**Location**: `cmd/engine/main.go:332` - `Stop()`

**Problem**:
- Single `stopChan` shared by multiple goroutines:
  - Health check loop (line 104)
  - Barrier sync loop (lines 206, 218)
  - Neighbor discovery loop (line 538)
- When closed, only one goroutine will read the signal
- Others may continue running indefinitely

**Impact**: Goroutines continue running after "stop", accumulating over restarts

### 4. Re-registration Without Cleanup
**Location**: `cmd/engine/main.go:170` - `attemptRegistration()`

**Problem**:
- Calls `startBarrierSyncLoop()` without stopping existing loops
- No check if a sync loop is already running
- Creates new goroutines on each re-registration attempt

**Impact**: Goroutine accumulation during network issues or controller restarts

## Potential Issues in Controller (Not Yet Confirmed)

### 1. Broadcast Step Goroutine Explosion
**Location**: `cmd/controller/main.go:151` - `broadcastStep()`

**Problem**:
- Spawns a new goroutine for each node on every step
- With 10 nodes at 50ms intervals = 200 goroutines/second

**Note**: This hasn't been observed causing issues yet, but could become problematic at scale.

## Memory Usage Breakdown

For a 7x7 grid (49 cells):
- Actual game state: 49 bits ≈ 7 bytes
- Expected memory with overhead: < 1 KB

Observed memory usage sources:
- Each goroutine: minimum 2 KB stack
- HTTP response buffers: ~4-8 KB each
- JSON decoding buffers: ~1-2 KB per request
- Accumulated goroutines: 10-20 per re-registration cycle
- Polling rate: 10 times/second (100ms ticker)

**Result**: 100+ MB in 2-3 minutes

## Proposed Fixes for Engine Pods

### Fix 1: Prevent Multiple Sync Loops
Add a flag to track if sync loop is running:
```go
type Engine struct {
    // ... existing fields ...
    syncLoopRunning bool
    syncLoopMutex   sync.Mutex
}
```

### Fix 2: Immediate Response Body Closure
Replace deferred closure with immediate closure:
```go
resp, err := e.httpClient.Get(endpoint + "/edges/" + requestDirection)
if err != nil {
    continue
}
if resp.StatusCode == 200 {
    json.NewDecoder(resp.Body).Decode(&e.edgeBuffer)
}
resp.Body.Close() // Immediate closure
```

### Fix 3: Separate Stop Channels
Create individual stop channels for each goroutine:
```go
type Engine struct {
    // ... existing fields ...
    healthStopChan    chan struct{}
    syncStopChan      chan struct{}
    neighborStopChan  chan struct{}
}
```

### Fix 4: Cleanup Before Re-registration
Ensure existing loops are stopped before starting new ones.

## Testing Strategy

1. Deploy fixed engine pod to single node
2. Monitor memory usage over 10 minutes
3. Simulate controller restarts to test re-registration
4. Verify goroutine count remains stable
5. Check for proper cleanup on pod termination

## Expected Outcome

After fixes:
- Memory usage should stabilize at < 10 MB per engine pod
- Goroutine count should remain constant (3-4 per pod)
- No memory growth during re-registrations
- Clean shutdown with all goroutines terminated