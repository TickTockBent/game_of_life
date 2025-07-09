#!/bin/bash

# Fast parallel build script for Game of Life

REGISTRY="${REGISTRY:-192.168.68.100:5000}"
VERSION="${VERSION:-latest}"

echo "🚀 Starting parallel multi-arch builds..."
echo "Registry: $REGISTRY"
echo "Version: $VERSION"

# Check if we're authenticated to the registry
echo "🔐 Checking registry authentication..."
if ! docker pull $REGISTRY/gameoflife-engine:latest &>/dev/null; then
    echo "⚠️  Not authenticated to registry, running auth setup..."
    export VAULT_ADDR='http://192.168.68.100:8200'
    if [ -f "./scripts/setup-registry-auth.sh" ]; then
        ./scripts/setup-registry-auth.sh
    else
        echo "❌ Error: setup-registry-auth.sh not found"
        exit 1
    fi
fi

# Ensure we have the right builder with insecure registry support
echo "🔧 Configuring buildx for insecure registry..."
if ! docker buildx inspect gameoflife-builder &>/dev/null; then
    echo "Creating buildx builder with insecure registry support..."
    cat > /tmp/buildkit.toml <<EOF
[registry."192.168.68.100:5000"]
  http = true
  insecure = true
EOF
    docker buildx create --name gameoflife-builder --driver docker-container --buildkitd-config /tmp/buildkit.toml --use --bootstrap
else
    docker buildx use gameoflife-builder
fi

# Function to build a single service
build_service() {
    local service=$1
    local dockerfile=$2
    
    echo "🔨 Building $service..."
    
    # Check if dockerfile exists
    if [ ! -f "$dockerfile" ]; then
        echo "❌ $service: Dockerfile $dockerfile not found"
        echo "1" > "/tmp/build_result_$service"
        return 1
    fi
    
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
    
    # Write result to temp file for parent process
    echo "$build_result" > "/tmp/build_result_$service"
    
    if [ $build_result -eq 0 ]; then
        echo "✅ $service completed in ${duration}s"
        return 0
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
wait $CONTROLLER_PID
wait $WEB_PID

# Read actual build results from temp files
ENGINE_RESULT=$(cat "/tmp/build_result_engine" 2>/dev/null || echo "1")
CONTROLLER_RESULT=$(cat "/tmp/build_result_controller" 2>/dev/null || echo "1")
WEB_RESULT=$(cat "/tmp/build_result_web" 2>/dev/null || echo "1")

# Cleanup temp files
rm -f "/tmp/build_result_engine" "/tmp/build_result_controller" "/tmp/build_result_web"

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