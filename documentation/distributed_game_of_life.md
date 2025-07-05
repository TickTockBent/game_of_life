# Distributed Conway's Game of Life on Kubernetes

## Project Overview

Build an interactive, distributed implementation of Conway's Game of Life that runs across a dynamically scaling Kubernetes cluster, with real-time web visualization showing which physical nodes are computing each section of the grid. Users can interactively scale the deployment and collaborate on the same synchronized grid in real-time.

## Architecture

### Cluster Infrastructure
- **Control Plane**: motherbrain (Intel E-2224, Ubuntu 24.04)
- **AMD64 Workers**: ridley, kraid (Beelink 5825U, Ubuntu 24.04)  
- **ARM64 Workers**: phantoon, crocomire, pihole-1, pihole-2 (Raspberry Pi, Debian 12)

### System Design

#### Grid Distribution
- **Dynamic grid topology** - K8s nodes own sections that appear/disappear as workers scale
- **Interactive scaling** - web UI buttons trigger actual K8s scaling events
- **Multi-user synchronization** - all users see the same live grid state
- **Graceful transitions** - grids fade in with random seeds, fade out when nodes drop
- **Real-time collaboration** - users can click to add/remove cells with instant sync

#### Components

**Game Engine Pods** (1 per node)
- Compute assigned grid section autonomously
- Expose REST API for state queries and cell updates
- Handle border cell synchronization with neighbors
- Report grid state and health to controller

**Controller Pod** (single instance)
- Aggregates grid data from all engine pods
- Handles user interactions and forwards to appropriate pods
- Manages K8s scaling requests from web interface
- Serves unified grid view to web clients
- Coordinates grid topology changes

**Web Frontend** 
- Dynamic grid visualization with zoom/pan
- Interactive cell placement with real-time sync
- Scaling controls (add/remove worker nodes)
- Node health and performance monitoring
- Smooth animations for grid transitions

**Ingress/Routing**
- Cloudflare tunnel exposure at `gameoflife.ticktockbent.com`
- Load balancing across healthy frontend instances
- Secure external access without port forwarding

## Features

### Core Functionality
- **Distributed Computation**: Game of Life rules executed across multiple physical machines
- **Dynamic Scaling**: Web UI controls actual K8s deployment scaling
- **Real-time Collaboration**: Multiple users interact with synchronized grid state
- **Topology Management**: Grids smoothly appear/disappear as nodes join/leave
- **Interactive Controls**: Click-to-place cells, scaling controls, play/pause

### Visualization
- **Dynamic Layout**: Auto-zoom and smooth transitions as grid topology changes
- **Node Boundaries**: Visual separators showing which physical node owns each section
- **Color Coding**: Different colors for ARM vs AMD64 nodes
- **Smooth Animations**: Grids fade/slide in when nodes join, fade out when leaving
- **Live Metrics**: Cells computed per node, generation rate, scaling events
- **Health Indicators**: Real-time status of each node's contribution

### Advanced Features
- **Interactive Scaling**: Users can scale K8s deployment up/down from web interface
- **Pattern Library**: Pre-loaded famous Conway patterns (Glider, Gosper Gun, etc.)
- **Failure Simulation**: Deliberately kill nodes to demonstrate resilience
- **Performance Comparison**: ARM vs AMD64 computation benchmarking
- **Multi-user Collaboration**: Synchronized grid state across all connected users
- **Future Enhancement**: Open source Docker participation for global grid

## Technical Implementation

### Backend Services

**Game Engine Container**
```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: gameoflife-engine
spec:
  template:
    spec:
      containers:
      - name: engine
        image: gameoflife/engine:latest
        env:
        - name: NODE_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        - name: GRID_SECTION
          value: "auto-assigned"
```

**Coordination Service**
```yaml
apiVersion: apps/v1  
kind: Deployment
metadata:
  name: gameoflife-coordinator
spec:
  replicas: 1
  template:
    spec:
      nodeSelector:
        kubernetes.io/arch: amd64  # Pin to Beelink for performance
```

**Frontend Application**
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gameoflife-web
spec:
  replicas: 2
  template:
    spec:
      containers:
      - name: frontend
        image: gameoflife/web:latest
        ports:
        - containerPort: 3000
```

### Communication Protocol

**Node Registration**
```json
{
  "nodeId": "ridley",
  "architecture": "amd64", 
  "gridSection": {"x": 0, "y": 0, "width": 100, "height": 100},
  "neighbors": ["motherbrain", "kraid"],
  "apiEndpoint": "http://ridley.gameoflife.svc.cluster.local:8080",
  "status": "joining"
}
```

**Grid State Query**
```json
{
  "nodeId": "ridley",
  "generation": 1247,
  "gridState": [[0,1,0,1],[1,1,0,1],[0,0,1,1]],
  "timestamp": "2024-01-01T12:00:00Z"
}
```

**User Interaction**
```json
{
  "action": "toggle_cell",
  "position": {"x": 150, "y": 200},
  "userId": "user123",
  "timestamp": "2024-01-01T12:00:01Z"
}
```

**Scaling Request**
```json
{
  "action": "scale_up",
  "targetReplicas": 5,
  "userId": "user123"
}
```

**Border Cell Sync**
```json
{
  "generation": 1247,
  "borders": {
    "north": [0,1,0,1,1,0,0,1],
    "south": [1,1,0,0,1,1,0,1], 
    "east": [0,1,1,0,1,0,1,1],
    "west": [1,0,1,1,0,0,1,0]
  }
}
```

### Web Interface

**Real-time Grid Display**
- Canvas-based rendering with smooth animations
- Dynamic zoom-out as new grids appear
- Node boundary overlays with labels 
- Click-to-toggle cell states with instant sync
- Smooth grid transitions (fade/slide in/out)

**Control Panel**
- Play/pause/step generation controls
- Speed adjustment slider
- Interactive scaling controls (add/remove nodes)
- Pattern spawning buttons
- Node health dashboard
- Multi-user indicator

**Performance Metrics**
- Generations per second
- Cells computed per node
- Network latency between nodes
- Resource utilization graphs

## Development Phases

### Phase 1: Core Engine (Week 1)
- [ ] Basic Game of Life algorithm implementation
- [ ] REST API for cell state queries
- [ ] Border synchronization protocol
- [ ] Single-node testing

### Phase 2: Distribution (Week 2)  
- [ ] Multi-node coordination service
- [ ] Grid section assignment logic
- [ ] Inter-node communication
- [ ] Kubernetes deployment manifests

### Phase 3: Web Interface (Week 3)
- [ ] Real-time WebSocket frontend
- [ ] Interactive grid visualization  
- [ ] Node monitoring dashboard
- [ ] Pattern library integration

### Phase 4: Advanced Features (Week 4)
- [ ] Failure detection and recovery
- [ ] Performance optimization
- [ ] Public deployment and testing
- [ ] Documentation and demos

## Success Metrics

**Technical Goals**
- Sustain 60+ generations per second across all nodes
- Sub-100ms border synchronization latency
- Graceful handling of 1-2 node failures
- Support grids up to 10,000x10,000 cells

**User Experience Goals**
- Responsive web interface with <50ms click-to-update
- Clear visualization of distributed computation
- Intuitive pattern spawning and interaction
- Educational value for distributed systems concepts

## Deployment

**Prerequisites**
- 7-node Kubernetes cluster (existing)
- Cloudflare tunnel configuration
- Container registry access
- Domain DNS configuration

**Installation**
```bash
# Deploy core services
kubectl apply -f manifests/namespace.yaml
kubectl apply -f manifests/coordinator.yaml  
kubectl apply -f manifests/engine-daemonset.yaml
kubectl apply -f manifests/frontend.yaml

# Configure ingress
kubectl apply -f manifests/ingress.yaml

# Verify deployment
kubectl get pods -n gameoflife
curl https://gameoflife.ticktockbent.com/health
```

## Educational Value

This project demonstrates several computer science concepts:

- **Distributed Systems**: Coordination, synchronization, failure handling
- **Cellular Automata**: Mathematical modeling and emergent behavior  
- **Kubernetes**: DaemonSets, Services, inter-pod communication
- **Real-time Web**: WebSockets, Canvas rendering, responsive UI
- **Multi-architecture Computing**: ARM vs AMD64 performance characteristics

## Future Enhancements

- **3D Game of Life**: Extend to volumetric cellular automata
- **Custom Rules**: Implement other cellular automaton rule sets
- **Machine Learning**: Pattern recognition and prediction
- **VR Visualization**: Immersive 3D grid exploration
- **Blockchain Integration**: Decentralized pattern verification

---

**Project Repository**: `https://github.com/ticktockbent/distributed-gameoflife`  
**Live Demo**: `https://gameoflife.ticktockbent.com`  
**Documentation**: `https://docs.gameoflife.ticktockbent.com`