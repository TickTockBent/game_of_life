# Distributed Conway's Game of Life - Development Guide

## Project Overview
A distributed implementation of Conway's Game of Life running across a dynamically scaling Kubernetes cluster with real-time web visualization. The system demonstrates distributed computing concepts by showing which physical nodes compute each section of the grid. Users can interactively scale the deployment and collaborate on the same synchronized grid in real-time.

## Architecture Summary
- **Dynamic grid topology** - grids appear/disappear as K8s workers scale
- **Game Engine Pods** (DaemonSet) compute grid sections autonomously
- **Controller Pod** aggregates data and handles user interactions
- **Web Frontend** provides interactive scaling and real-time collaboration
- **Multi-user synchronization** - all users see the same live grid state
- **Graceful transitions** - grids fade in/out with scaling events

## Key Technologies
- **Backend**: Go/Rust for game engine, REST APIs
- **Frontend**: JavaScript/TypeScript, WebSockets, Canvas
- **Orchestration**: Kubernetes DaemonSets, Services, Ingress
- **Communication**: REST API, WebSocket, JSON protocols
- **Deployment**: Container images, Cloudflare tunnels

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

### Game Engine
- Implements Conway's Game of Life rules
- Manages assigned grid section autonomously
- Exposes REST API for state queries and cell updates
- Handles border cell synchronization with neighbors
- Reports grid state and health to controller

### Controller Pod
- Aggregates grid data from all engine pods
- Handles user interactions and forwards to appropriate pods
- Manages K8s scaling requests from web interface
- Serves unified grid view to web clients
- Coordinates grid topology changes during scaling

### Web Frontend
- Dynamic grid visualization with smooth animations
- Interactive cell placement with real-time sync across users
- Scaling controls that trigger actual K8s deployment changes
- Node health and performance monitoring
- Pattern library with famous Conway configurations
- Auto-zoom and transitions as grid topology changes

## Development Phases
1. **Core Engine** - Basic Game of Life implementation and REST API
2. **Controller Integration** - Aggregation service and user interaction handling
3. **Web Interface** - Dynamic visualization with scaling controls
4. **Advanced Features** - Multi-user sync, smooth transitions, K8s scaling
5. **Future Enhancement** - Open source Docker participation for global grid
6. **Scaling Architecture** - Hierarchical regions (10x10 grids of 7x7 workers)
7. **Barrier Synchronization** - Timing system where nodes signal ready, advance when all report complete

## Performance Targets
- 60+ generations per second across all nodes
- Sub-100ms border synchronization latency
- Support for 10,000x10,000 cell grids
- Graceful handling of dynamic scaling events
- Sub-50ms user interaction response time
- Smooth animations during topology changes

## Testing Strategy
- Unit tests for game logic
- Integration tests for REST API
- End-to-end tests for distributed coordination
- Multi-user synchronization tests
- Scaling event simulation tests
- Performance benchmarks for ARM vs AMD64 nodes
- UI interaction and animation tests

## Deployment
- Kubernetes manifests for all services
- Cloudflare tunnel for external access
- Container registry for image distribution
- Health checks and monitoring

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