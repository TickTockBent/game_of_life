#!/bin/bash

# Build multi-architecture images for Game of Life

REGISTRY="${REGISTRY:-registry.ticktockbent.com}"
VERSION="${VERSION:-latest}"

echo "Building multi-architecture Game of Life images"
echo "Target architectures: linux/amd64, linux/arm64"

# Ensure buildx is available
if ! docker buildx version &>/dev/null; then
    echo "❌ Docker buildx not found. Please install Docker Desktop or enable buildx"
    exit 1
fi

# Create or use buildx builder
BUILDER_NAME="gameoflife-builder"
if ! docker buildx ls | grep -q $BUILDER_NAME; then
    echo "Creating buildx builder..."
    docker buildx create --name $BUILDER_NAME --use
    docker buildx inspect --bootstrap
else
    echo "Using existing buildx builder..."
    docker buildx use $BUILDER_NAME
fi

# Function to build and push multi-arch images
build_multiarch() {
    local component=$1
    local dockerfile=$2
    
    echo ""
    echo "Building $component for linux/amd64,linux/arm64..."
    
    docker buildx build \
        --platform linux/amd64,linux/arm64 \
        --file $dockerfile \
        --tag $REGISTRY/gameoflife-$component:$VERSION \
        --push \
        .
        
    if [ $? -eq 0 ]; then
        echo "✅ Successfully built and pushed multi-arch $component"
    else
        echo "❌ Failed to build $component"
        exit 1
    fi
}

echo ""
echo "🔨 Building multi-architecture images..."

# Build all components
build_multiarch "engine" "Dockerfile.engine"
build_multiarch "controller" "Dockerfile.controller" 
build_multiarch "web" "Dockerfile.web"

echo ""
echo "🎉 All multi-architecture images built and pushed successfully!"
echo ""
echo "Images available for both AMD64 and ARM64:"
echo "  $REGISTRY/gameoflife-engine:$VERSION"
echo "  $REGISTRY/gameoflife-controller:$VERSION"
echo "  $REGISTRY/gameoflife-web:$VERSION"