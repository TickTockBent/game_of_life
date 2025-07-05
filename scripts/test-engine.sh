#!/bin/bash

echo "Testing Game of Life Engine API..."

BASE_URL="http://localhost:8080"

echo "1. Checking health..."
curl -s $BASE_URL/health | jq .

echo -e "\n2. Getting initial state..."
curl -s $BASE_URL/state | jq '.generation'

echo -e "\n3. Randomizing grid..."
curl -s -X POST $BASE_URL/randomize

echo -e "\n4. Getting randomized state..."
curl -s $BASE_URL/state | jq '.grid'

echo -e "\n5. Stepping one generation..."
curl -s -X POST $BASE_URL/step

echo -e "\n6. Getting new generation..."
curl -s $BASE_URL/state | jq '.generation'

echo -e "\n7. Setting a specific cell..."
curl -s -X POST $BASE_URL/cell -H "Content-Type: application/json" -d '{"x":3,"y":3,"alive":true}'

echo -e "\n8. Starting auto-generation..."
curl -s -X POST $BASE_URL/start

echo -e "\n9. Waiting 1 second..."
sleep 1

echo -e "\n10. Getting state after auto-generation..."
curl -s $BASE_URL/state | jq '.generation'

echo -e "\n11. Stopping auto-generation..."
curl -s -X POST $BASE_URL/stop

echo -e "\nTest complete!"