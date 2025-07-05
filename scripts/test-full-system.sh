#!/bin/bash

echo "Testing Full Distributed Game of Life System..."

# Kill any existing processes
pkill -f "bin/controller" || true
pkill -f "bin/engine" || true
pkill -f "bin/web" || true
sleep 1

# Start controller
echo "Starting controller..."
PORT=9081 REGION_ID=test ./bin/controller &
CONTROLLER_PID=$!

# Wait for controller to start
sleep 2

# Get the machine's IP address
MACHINE_IP=$(hostname -I | awk '{print $1}')

# Start engines
echo "Starting engine nodes..."
PORT=9080 NODE_NAME=node1 CONTROLLER_URL=http://$MACHINE_IP:9081 SELF_ENDPOINT=http://$MACHINE_IP:9080 ./bin/engine &
ENGINE1_PID=$!

PORT=9082 NODE_NAME=node2 CONTROLLER_URL=http://$MACHINE_IP:9081 SELF_ENDPOINT=http://$MACHINE_IP:9082 ./bin/engine &
ENGINE2_PID=$!

PORT=9083 NODE_NAME=node3 CONTROLLER_URL=http://$MACHINE_IP:9081 SELF_ENDPOINT=http://$MACHINE_IP:9083 ./bin/engine &
ENGINE3_PID=$!

# Wait for engines to start and register
sleep 3

# Start web server
echo "Starting web visualizer..."
PORT=9084 CONTROLLER_URL=http://$MACHINE_IP:9081 ./bin/web &
WEB_PID=$!

# Wait for web server to start
sleep 2

echo -e "\n=== System Status ==="
echo "Controller health:"
curl -s http://$MACHINE_IP:9081/health

echo -e "\nController topology:"
curl -s http://$MACHINE_IP:9081/topology

echo -e "\n=== Testing Web API ==="
echo "Web server topology:"
curl -s http://$MACHINE_IP:9084/api/topology

echo -e "\nWeb server grid state:"
curl -s http://$MACHINE_IP:9084/api/grid

echo -e "\n=== Testing Cell Click ==="
echo "Clicking cell at global position (3,3):"
curl -s -X POST http://$MACHINE_IP:9084/api/click \
  -H "Content-Type: application/json" \
  -d '{"globalX":3,"globalY":3,"alive":true}'

echo -e "\n=== System Ready ==="
echo "🎮 Web interface available at: http://$(hostname -I | awk '{print $1}'):9084"
echo "📊 Controller API at: http://localhost:9081"
echo "🔧 Engine APIs at: http://localhost:9080, http://localhost:9082, http://localhost:9083"
echo ""
echo "Press Enter to stop all services..."
read

# Cleanup
echo "Cleaning up..."
kill $CONTROLLER_PID $ENGINE1_PID $ENGINE2_PID $ENGINE3_PID $WEB_PID 2>/dev/null || true

echo "Test complete!"