#!/bin/bash

# Setup Docker registry authentication for Game of Life deployment

echo "Setting up registry authentication for Game of Life..."

# Check if namespace exists
if ! kubectl get namespace gameoflife &>/dev/null; then
    echo "Creating gameoflife namespace..."
    kubectl create namespace gameoflife
fi

# Check if VAULT_ADDR is set
if [ -z "$VAULT_ADDR" ]; then
    echo "Setting VAULT_ADDR to default..."
    export VAULT_ADDR='http://127.0.0.1:8200'
fi

# Check Vault connectivity
if ! vault status &>/dev/null; then
    echo "❌ Cannot connect to Vault at $VAULT_ADDR"
    echo "Please ensure Vault is accessible and you're authenticated"
    exit 1
fi

# Get credentials from Vault
echo "Retrieving credentials from Vault..."
CREDS=$(vault kv get -format=json docker-registry/creds 2>/dev/null)

if [ $? -ne 0 ]; then
    echo "❌ Failed to retrieve credentials from Vault"
    echo "Please ensure you have access to docker-registry/creds in Vault"
    exit 1
fi

USERNAME=$(echo $CREDS | jq -r '.data.data.username')
PASSWORD=$(echo $CREDS | jq -r '.data.data.password')

if [ -z "$USERNAME" ] || [ -z "$PASSWORD" ]; then
    echo "❌ Failed to parse credentials from Vault response"
    exit 1
fi

# Login to Docker registry
echo "Logging in to registry.ticktockbent.com..."
echo $PASSWORD | docker login registry.ticktockbent.com -u $USERNAME --password-stdin

if [ $? -ne 0 ]; then
    echo "❌ Failed to login to Docker registry"
    exit 1
fi

# Create Kubernetes secret for image pulls
echo "Creating image pull secret in gameoflife namespace..."
kubectl create secret docker-registry registry-secret \
  --docker-server=registry.ticktockbent.com \
  --docker-username=$USERNAME \
  --docker-password=$PASSWORD \
  --docker-email=admin@ticktockbent.com \
  -n gameoflife \
  --dry-run=client -o yaml | kubectl apply -f -

if [ $? -eq 0 ]; then
    echo "✅ Successfully created registry-secret in gameoflife namespace"
else
    echo "❌ Failed to create registry secret"
    exit 1
fi

echo ""
echo "✅ Registry authentication setup complete!"
echo ""
echo "You can now:"
echo "1. Build and push images: make docker-build"
echo "2. Deploy to Kubernetes: kubectl apply -f manifests/"