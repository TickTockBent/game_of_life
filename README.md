# Distributed Conway's Game of Life

A distributed implementation of Conway's Game of Life running on Kubernetes, demonstrating distributed computing concepts with real-time visualization.

## Current Status

✅ **Core Components Implemented:**
- Game engine with REST API
- Controller service for aggregation
- Web frontend with basic visualization
- Kubernetes manifests (DaemonSet, Services, Ingress)
- Docker registry integration at registry.ticktockbent.com
- Vault-based authentication for image pulls

🚧 **In Progress:**
- Debugging pod crashes in initial deployment
- Border synchronization between nodes
- Real-time WebSocket updates
- Interactive scaling controls

## Quick Start

### Prerequisites
- Kubernetes cluster (k3s/k8s)
- Docker
- Go 1.21+
- Access to Vault for registry authentication
- kubectl configured for your cluster

### Deployment

```bash
# Ensure Vault authentication is configured
export VAULT_ADDR='http://127.0.0.1:8200'
vault login  # if needed

# Deploy everything (builds, pushes, and deploys)
make deploy
```

This will:
1. Set up registry authentication via Vault
2. Build all Docker images
3. Push to registry.ticktockbent.com
4. Deploy to your Kubernetes cluster

### Manual Steps

```bash
# Build binaries
make build

# Build Docker images
make docker-build

# Push to registry
make docker-push

# Set up authentication only
make setup-auth

# Deploy to cluster
kubectl apply -f manifests/
```

### Development

```bash
# Run local test
make local-test

# Run tests
make test

# Check deployment status
kubectl get pods -n gameoflife
kubectl logs -f -n gameoflife deployment/gameoflife-controller
```

## Architecture

- **Game Engine** (DaemonSet): One pod per node computing grid sections
- **Controller**: Aggregates data from engines and handles user interactions
- **Web Frontend**: Interactive visualization and controls
- **Registry**: Private Docker registry with Vault authentication

## Project Structure

```
.
├── cmd/                    # Application entrypoints
│   ├── engine/            # Game engine service
│   ├── controller/        # Controller service
│   └── web/              # Web frontend server
├── pkg/                   # Shared packages
│   ├── gameoflife/       # Core game logic
│   ├── grid/             # Grid management
│   ├── controller/       # Controller logic
│   └── api/              # API models
├── web/                   # Frontend assets
│   └── public/           # Static files
├── manifests/            # Kubernetes YAML
├── scripts/              # Helper scripts
└── documentation/        # Project docs
```

## Troubleshooting

### Image Pull Issues
- Ensure registry secret exists: `kubectl get secret registry-secret -n gameoflife`
- Re-run authentication: `make setup-auth`
- Check Vault connectivity: `vault status`

### Pod Crashes
- Check logs: `kubectl logs -n gameoflife <pod-name>`
- Verify environment variables in manifests
- Ensure services are accessible within cluster

## Contributing

See [CLAUDE.md](./CLAUDE.md) for development guidelines and project conventions.