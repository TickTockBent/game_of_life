#!/bin/bash

echo "Testing Distributed Game of Life System..."

# Kill any existing processes
pkill -f "bin/controller" || true
pkill -f "bin/engine" || true
sleep 1

# Start controller
echo "Starting controller..."
PORT=9081 REGION_ID=test ./bin/controller &
CONTROLLER_PID=$!

# Wait for controller to start
sleep 2

# Start first engine
echo "Starting first engine..."
PORT=9080 NODE_NAME=node1 CONTROLLER_URL=http://localhost:9081 SELF_ENDPOINT=http://localhost:9080 ./bin/engine &
ENGINE1_PID=$!

# Wait for engine to start
sleep 2

# Start second engine
echo "Starting second engine..."
PORT=9082 NODE_NAME=node2 CONTROLLER_URL=http://localhost:9081 SELF_ENDPOINT=http://localhost:9082 ./bin/engine &
ENGINE2_PID=$!

# Wait for engines to start
sleep 2

echo -e "\n=== Testing Controller ==="
echo "Controller health:"
curl -s http://localhost:9081/health

echo -e "\nController topology:"
curl -s http://localhost:9081/topology

echo -e "\n=== Testing Engine Registration ==="
echo "Engine 1 health:"
curl -s http://localhost:9080/health

echo "Engine 2 health:"
curl -s http://localhost:9082/health

echo -e "\n=== Testing Edge Sharing ==="
echo "Engine 1 north edge:"
curl -s http://localhost:9080/edges/north

echo "Engine 2 south edge:"
curl -s http://localhost:9082/edges/south

echo -e "\n=== Testing Neighbor Discovery ==="
echo "Getting neighbors for position 0:"
curl -s http://localhost:9081/neighbors/0

echo "Getting neighbors for position 1:"
curl -s http://localhost:9081/neighbors/1

# Cleanup
echo -e "\nCleaning up..."
kill $CONTROLLER_PID $ENGINE1_PID $ENGINE2_PID 2>/dev/null || true

echo "Test complete!"