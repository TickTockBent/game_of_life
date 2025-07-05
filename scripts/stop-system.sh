#!/bin/bash

echo "Stopping Distributed Game of Life System..."

pkill -f "bin/controller" && echo "Controller stopped"
pkill -f "bin/engine" && echo "Engines stopped" 
pkill -f "bin/web" && echo "Web server stopped"

echo "All services stopped."