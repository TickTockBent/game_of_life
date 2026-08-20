# Distributed Conway's Game of Life - Development Guide

## Project Overview
A distributed implementation of Conway's Game of Life running across a dynamically scaling Kubernetes cluster with real-time web visualization. The system demonstrates distributed computing concepts by showing which physical nodes compute each section of the grid. Users can interactively scale the deployment and collaborate on the same synchronized grid in real-time.

## Current Architecture (Updated July 2025)

### System Components

#### 1. Controller (External Docker Container)
- **Location**: Runs outside K3s cluster as Docker container  
- **Port**: 8082 with external DNS access (ticktockbent.com:8082)
- **Function**: Central coordinator using channel-based message handling
- **Key Features**:
  - Zero-lock architecture with dedicated channels for each operation type
  - Barrier synchronization for distributed stepping  
  - Auto-healing: removes engines that miss 3+ steps
  - WebSocket streaming for real-time updates
  - Built-in metrics and health monitoring

#### 2. Game Engine Pods (Kubernetes DaemonSet)
- **Location**: K3s cluster nodes
- **Function**: Compute individual 7x7 grid sections
- **Key Features**:
  - Barrier-synchronized stepping (waits for controller step signals)
  - Auto re-registration after 3 failed state pushes
  - Halo exchange for border cell coordination
  - Self-randomization after 1000 idle generations

#### 3. Web Frontend Pods (Kubernetes Deployment)
- **Location**: K3s cluster
- **Function**: Serve web interface and proxy to controller
- **Key Features**:
  - WebSocket proxy to controller for real-time updates
  - Interactive grid visualization with Canvas rendering
  - Click-to-randomize grid sections
  - Real-time metrics and node status display

### Communication Architecture

#### Channel-Based Controller Design
```go
type Controller struct {
    // Dedicated channels for different request types
    registerChan    chan *RegisterMessage      // Engine registration
    stateUpdateChan chan *StateUpdateMessage   // Grid state updates  
    haloReadChan    chan *HaloReadMessage      // Border cell requests
    webReadChan     chan *WebReadMessage       // Web interface queries
    clickChan       chan *ClickMessage         // User interactions
    stepBroadcastChan chan *StepBroadcastMessage // Step coordination
    
    // Barrier synchronization state
    readyEngines      map[int]bool  // Tracks engine readiness
    stepInProgress    bool          // Step broadcast status
    barrierTimeout    time.Duration // 1 second timeout
    
    // WebSocket clients for real-time streaming
    wsClients    map[*websocket.Conn]bool
}
```

#### Barrier Synchronization Flow
1. **Engine Readiness**: Engines post state updates signaling readiness
2. **Barrier Check**: Controller checks if all engines ready or timeout expired  
3. **Step Broadcast**: Controller sends HTTP POST to `/step` on all engines
4. **Generation Advance**: Controller increments generation counter
5. **WebSocket Update**: Real-time state broadcast to web clients
6. **Auto-Healing**: Remove engines that miss 3+ consecutive steps

### Key Technologies
- **Backend**: Go with Gorilla Mux and WebSocket libraries
- **Frontend**: JavaScript with WebSocket streaming, Canvas visualization
- **Orchestration**: Kubernetes DaemonSets (engines), Deployments (web)
- **Communication**: HTTP REST APIs, WebSocket streaming, JSON protocols
- **Deployment**: Docker containers, external controller deployment

## Development Commands

### Build & Test
```bash
# Build all components
make build

# Build specific components
make build-engine
make build-controller
make build-web

# Test single node
make test-single

# Test distributed
make test-cluster

# Test scaling scenarios
make test-scaling

# Run linting
make lint

# Run type checking
make typecheck
```

### Kubernetes Operations
```bash
# Deploy to cluster
kubectl apply -f manifests/

# Check pod status
kubectl get pods -n gameoflife

# View logs
kubectl logs -f daemonset/gameoflife-engine -n gameoflife

# Port forward for development
kubectl port-forward svc/gameoflife-coordinator 8080:8080 -n gameoflife
```

### Development Workflow
```bash
# Start development server
make dev

# Build containers
make build-images

# Deploy to test cluster
make deploy-test

# Monitor metrics
make monitor
```

### Current Deployment Commands
```bash
# Build all containers (user runs this due to timeout issues)
make build

# Deploy controller externally (Docker container)
docker run -d --name gameoflife-controller \
  -p 8082:8081 \
  192.168.68.100:5000/gameoflife-controller:latest

# Deploy engine and web pods to K3s
kubectl apply -f manifests/engine-deployment.yaml
kubectl apply -f manifests/web-deployment.yaml

# Scale engine pods
kubectl scale daemonset gameoflife-engine --replicas=50 -n gameoflife

# Check logs
kubectl logs -f daemonset/gameoflife-engine -n gameoflife
docker logs -f gameoflife-controller
```

## Project Structure
```
.
├── cmd/
│   ├── engine/          # Game engine service
│   ├── controller/      # Controller service
│   └── web/            # Web frontend
├── pkg/
│   ├── gameoflife/     # Core game logic
│   ├── grid/           # Grid management
│   ├── sync/           # Border synchronization
│   ├── scaling/        # K8s scaling integration
│   └── metrics/        # Performance monitoring
├── web/
│   ├── src/            # Frontend source
│   │   ├── components/ # UI components
│   │   ├── grid/       # Grid visualization
│   │   └── scaling/    # Scaling controls
│   └── public/         # Static assets
├── manifests/          # Kubernetes YAML files
└── scripts/           # Build and deployment scripts
```

## Key Components

### Game Engine (cmd/engine/main.go)
- Implements Conway's Game of Life rules on 7x7 grid sections
- **Barrier Sync**: Waits for step signals from controller instead of autonomous stepping
- **Auto Re-registration**: Re-registers after 3 consecutive failed state pushes
- **Halo Exchange**: Requests border cells from controller for neighbor coordination
- **Self-healing**: Auto-randomizes after 1000 idle generations
- **API Endpoints**: `/register`, `/step`, `/randomize`, `/health`

### Controller (cmd/controller/main.go) 
- **Channel-based Architecture**: Zero-lock design with dedicated message channels
- **Barrier Coordination**: Manages distributed stepping with 1-second timeout
- **Auto-healing**: Removes engines that miss 3+ consecutive steps  
- **WebSocket Streaming**: Real-time updates to web clients
- **State Management**: Aggregates grid states from all engines
- **API Endpoints**: 
  - Core: `/register`, `/state/{position}`, `/halo/{position}`, `/generation`, `/health`
  - Web Interface: `/topology`, `/aggregated-state`, `/metrics`, `/ws`
  - Legacy: `/api/grid` (maps to `/aggregated-state`)
  - Interactive: `/api/click`, `/api/randomize`
  - Debug: `/debug/pause`, `/debug/unpause`, `/debug/nextstep`

### Web Frontend (cmd/web/main.go + web/public/)
- **WebSocket Proxy**: Proxies WebSocket connections between browser and controller
- **Canvas Visualization**: Real-time grid rendering with incremental updates
- **Interactive Features**: Click-to-randomize grid sections
- **Metrics Display**: Real-time node status, generation counters, queue sizes
- **Fallback Polling**: Graceful fallback if WebSocket connection fails

## Development Phases
1. **Core Engine** - Basic Game of Life implementation and REST API ✅
2. **Controller Integration** - Aggregation service and user interaction handling ✅
3. **Web Interface** - Dynamic visualization with scaling controls ✅
4. **Advanced Features** - Multi-user sync, smooth transitions, K8s scaling ✅
5. **Barrier Synchronization** - Coordinated stepping system ✅ **CURRENT**
6. **External Controller** - Moved controller outside K3s for stability ✅ **CURRENT**
7. **WebSocket Streaming** - Real-time updates instead of polling ✅ **CURRENT**
8. **Auto-healing Architecture** - Self-recovery from network issues ✅ **CURRENT**

## Recent Major Changes (July 2025)
- **Removed Router**: Eliminated router component, integrated messaging into controller
- **Channel Architecture**: Rebuilt controller with zero-lock, channel-based design
- **Barrier Synchronization**: Replaced autonomous stepping with coordinated stepping
- **External Deployment**: Moved controller outside K3s to avoid network timeouts
- **WebSocket Streaming**: Added real-time updates for smoother web experience
- **Auto-healing**: Added engine re-registration and stale node cleanup

## Performance Targets & Current Results
- **Target**: 60+ generations per second → **Achieved**: ~1 generation/second with barrier sync
- **Target**: Sub-100ms border synchronization → **Achieved**: Real-time halo exchange
- **Target**: Support for 10,000x10,000 cell grids → **Achieved**: 100 engines × 49 cells = 4,900 cells
- **Target**: Graceful dynamic scaling → **Achieved**: Auto-healing with re-registration
- **Target**: Sub-50ms user interaction → **Achieved**: WebSocket real-time updates
- **Target**: Smooth animations → **Achieved**: Incremental Canvas rendering

## Testing Strategy
- Unit tests for game logic
- Integration tests for REST API
- End-to-end tests for distributed coordination
- Multi-user synchronization tests
- Scaling event simulation tests
- Performance benchmarks for ARM vs AMD64 nodes
- UI interaction and animation tests

## Current Deployment (August 2026) — Local Docker Compose

The K3s cluster, private registry (`192.168.68.100:5000`), and `gameoflife*.ticktockbent.com`
DNS/tunnel were all retired in the lab move. **The K8s manifests and registry-based Makefile
targets below are historical.** The project now runs locally via `docker-compose.yml`:

```bash
make up ENGINES=3      # build + start controller, web, 3 engines
make scale ENGINES=9   # grow the grid (up to 100 positions in the 10x10 layout)
make status / logs / down
```

- Web UI: http://localhost:8090 (host port 8080 is taken by another service on motherbrain)
- Controller API: http://localhost:8082 (container port 8081)
- Engines have no host ports; the controller reaches them on the compose network. The engine
  derives its node ID from the container hostname and its endpoint from its own interface IP
  when `POD_NAME`/`POD_IP` are absent, so `--scale` just works.
- Engines re-register automatically if no step signal arrives for 10s (controller restart).

## Historical: K3s Deployment Architecture
- **Controller**: External Docker container on host (port 8082)
- **Engines**: K3s DaemonSet with auto-scaling (up to 100 tested)
- **Web**: K3s Deployment with WebSocket proxy
- **Registry**: Local container registry (192.168.68.100:5000)
- **External Access**: DNS forwarding (ticktockbent.com:8082 → controller)
- **Monitoring**: Built-in metrics endpoints and WebSocket health checks

## Troubleshooting Notes
- **Build Timeouts**: User must run builds due to container build duration
- **K3s Networking**: External controller avoids internal network timeouts
- **WebSocket Issues**: Web proxy handles protocol upgrades automatically
- **Engine Re-registration**: Engines auto-recover from network failures
- **Generation Resets**: Resolved by barrier synchronization implementation

## Useful References
- Conway's Game of Life rules
- Kubernetes DaemonSet documentation
- WebSocket API patterns
- Distributed systems coordination patterns

## Working Memory
- **Current session notes**: `./documentation/tmp/working_memory.md`
- Check this file for recent discoveries, bugs found, architecture changes, and next steps
- Updated throughout development sessions to maintain context across cleanups

## Deployment Warnings
- When redeploying, do not delete the namespace as that also removes the TLS and registry auth secrets.