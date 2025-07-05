#!/bin/bash

echo "Starting Distributed Game of Life System..."

# Kill any existing processes
pkill -f "bin/controller" || true
pkill -f "bin/engine" || true
pkill -f "bin/web" || true
sleep 1

# Get the machine's IP address
MACHINE_IP=$(hostname -I | awk '{print $1}')

echo "Using machine IP: $MACHINE_IP"

# Start controller
echo "Starting controller..."
PORT=9081 REGION_ID=production ./bin/controller > /tmp/controller.log 2>&1 &
CONTROLLER_PID=$!
echo "Controller PID: $CONTROLLER_PID"

# Wait for controller to start
sleep 3

# Start engines
echo "Starting engine nodes..."
PORT=9080 NODE_NAME=node1 CONTROLLER_URL=http://$MACHINE_IP:9081 SELF_ENDPOINT=http://$MACHINE_IP:9080 ./bin/engine > /tmp/engine1.log 2>&1 &
ENGINE1_PID=$!
echo "Engine 1 PID: $ENGINE1_PID"

PORT=9082 NODE_NAME=node2 CONTROLLER_URL=http://$MACHINE_IP:9081 SELF_ENDPOINT=http://$MACHINE_IP:9082 ./bin/engine > /tmp/engine2.log 2>&1 &
ENGINE2_PID=$!
echo "Engine 2 PID: $ENGINE2_PID"

PORT=9083 NODE_NAME=node3 CONTROLLER_URL=http://$MACHINE_IP:9081 SELF_ENDPOINT=http://$MACHINE_IP:9083 ./bin/engine > /tmp/engine3.log 2>&1 &
ENGINE3_PID=$!
echo "Engine 3 PID: $ENGINE3_PID"

# Wait for engines to register
sleep 5

# Start web server
echo "Starting web visualizer..."
PORT=9084 CONTROLLER_URL=http://$MACHINE_IP:9081 ./bin/web > /tmp/web.log 2>&1 &
WEB_PID=$!
echo "Web Server PID: $WEB_PID"

# Wait for web server to start
sleep 3

echo ""
echo "=== System Started ==="
echo "🎮 Web interface: http://$MACHINE_IP:9084"
echo "📊 Controller API: http://$MACHINE_IP:9081"
echo "🔧 Engine APIs: http://$MACHINE_IP:9080, http://$MACHINE_IP:9082, http://$MACHINE_IP:9083"
echo ""
echo "Process IDs:"
echo "  Controller: $CONTROLLER_PID"
echo "  Engine 1:   $ENGINE1_PID"
echo "  Engine 2:   $ENGINE2_PID"
echo "  Engine 3:   $ENGINE3_PID"
echo "  Web Server: $WEB_PID"
echo ""
echo "Logs available at:"
echo "  /tmp/controller.log"
echo "  /tmp/engine1.log"
echo "  /tmp/engine2.log"
echo "  /tmp/engine3.log"
echo "  /tmp/web.log"
echo ""
echo "To stop all services, run: ./scripts/stop-system.sh"
echo ""

# Test if everything is responding
echo "Testing system health..."
sleep 2

echo -n "Controller: "
if curl -s http://$MACHINE_IP:9081/health > /dev/null; then
    echo "✅ OK"
else
    echo "❌ FAILED"
fi

echo -n "Web Server: "
if curl -s http://$MACHINE_IP:9084/api/topology > /dev/null; then
    echo "✅ OK"
else
    echo "❌ FAILED"
fi

echo ""
echo "System is ready! Access the web interface at: http://$MACHINE_IP:9084"