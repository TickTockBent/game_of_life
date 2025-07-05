#!/bin/bash

# Configuration for your production registry setup
REGISTRY="${REGISTRY:-registry.ticktockbent.com}"
VERSION="${VERSION:-latest}"

echo "Building and pushing Game of Life images to $REGISTRY"
echo "Using Vault-managed authentication..."

# Check if registry is accessible
if ! curl -s https://$REGISTRY/v2/ > /dev/null; then
    echo "⚠️  Registry not accessible at $REGISTRY"
    echo "Make sure registry is running and accessible from this machine"
    echo "Alternative: Use localhost:5000 with port-forward for local development"
    exit 1
fi

# Function to build and push images (single arch for now)
build_and_push() {
    local component=$1
    local dockerfile=$2
    
    echo "Building $component..."
    
    # Build for current architecture
    docker build \
        --file $dockerfile \
        --tag $REGISTRY/gameoflife-$component:$VERSION \
        .
        
    if [ $? -eq 0 ]; then
        echo "✅ Successfully built $component"
        docker push $REGISTRY/gameoflife-$component:$VERSION
        if [ $? -eq 0 ]; then
            echo "✅ Successfully pushed $component"
        else
            echo "❌ Failed to push $component"
            exit 1
        fi
    else
        echo "❌ Failed to build $component"
        exit 1
    fi
}

# Note: Building for current architecture only
# Multi-arch builds can be enabled later with buildx

echo "Building Game of Life components..."

# Build all components
build_and_push "engine" "Dockerfile.engine"
build_and_push "controller" "Dockerfile.controller" 
build_and_push "web" "Dockerfile.web"

echo ""
echo "🎉 All images built and pushed successfully!"
echo ""
echo "Images available:"
echo "  $REGISTRY/gameoflife-engine:$VERSION"
echo "  $REGISTRY/gameoflife-controller:$VERSION"
echo "  $REGISTRY/gameoflife-web:$VERSION"
echo ""
echo "Images accessible from all cluster nodes via: $REGISTRY"
echo "Ready to deploy to Kubernetes!"