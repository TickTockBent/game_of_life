#!/bin/bash

# Fast parallel build script for Game of Life

REGISTRY="${REGISTRY:-registry.ticktockbent.com}"
VERSION="${VERSION:-latest}"

echo "🚀 Starting parallel multi-arch builds..."
echo "Registry: $REGISTRY"
echo "Version: $VERSION"

# Function to build a single service
build_service() {
    local service=$1
    local dockerfile=$2
    
    echo "🔨 Building $service..."
    
    start_time=$(date +%s)
    
    docker buildx build \
        --platform linux/amd64,linux/arm64 \
        --file "$dockerfile" \
        --tag "$REGISTRY/gameoflife-$service:$VERSION" \
        --push \
        . 2>&1 | sed "s/^/[$service] /"
    
    build_result=$?
    end_time=$(date +%s)
    duration=$((end_time - start_time))
    
    if [ $build_result -eq 0 ]; then
        echo "✅ $service completed in ${duration}s"
    else
        echo "❌ $service failed after ${duration}s"
        return 1
    fi
}

# Start all builds in parallel
echo "Starting parallel builds..."

build_service "engine" "Dockerfile.engine.optimized" &
ENGINE_PID=$!

build_service "controller" "Dockerfile.controller.optimized" &
CONTROLLER_PID=$!

build_service "web" "Dockerfile.web.optimized" &
WEB_PID=$!

# Wait for all builds to complete
echo "Waiting for builds to complete..."

wait $ENGINE_PID
ENGINE_RESULT=$?

wait $CONTROLLER_PID
CONTROLLER_RESULT=$?

wait $WEB_PID
WEB_RESULT=$?

# Report results
echo ""
echo "📊 Build Results:"
[ $ENGINE_RESULT -eq 0 ] && echo "✅ Engine: Success" || echo "❌ Engine: Failed"
[ $CONTROLLER_RESULT -eq 0 ] && echo "✅ Controller: Success" || echo "❌ Controller: Failed"  
[ $WEB_RESULT -eq 0 ] && echo "✅ Web: Success" || echo "❌ Web: Failed"

if [ $ENGINE_RESULT -eq 0 ] && [ $CONTROLLER_RESULT -eq 0 ] && [ $WEB_RESULT -eq 0 ]; then
    echo ""
    echo "🎉 All builds completed successfully!"
    echo ""
    echo "Images ready:"
    echo "  $REGISTRY/gameoflife-engine:$VERSION"
    echo "  $REGISTRY/gameoflife-controller:$VERSION"  
    echo "  $REGISTRY/gameoflife-web:$VERSION"
    exit 0
else
    echo ""
    echo "💥 Some builds failed!"
    exit 1
fi