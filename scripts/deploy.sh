#!/bin/bash

echo "Deploying Game of Life to Kubernetes..."

# Check if kubectl is available
if ! command -v kubectl &> /dev/null; then
    echo "❌ kubectl is required but not installed"
    exit 1
fi

# Check if we can connect to the cluster
if ! kubectl cluster-info &> /dev/null; then
    echo "❌ Cannot connect to Kubernetes cluster"
    exit 1
fi

echo "✅ Connected to Kubernetes cluster"
kubectl cluster-info | head -1

# Deploy in order
echo ""
echo "📦 Deploying namespace..."
kubectl apply -f manifests/namespace.yaml

echo ""
echo "🎮 Deploying controller..."
kubectl apply -f manifests/controller-deployment.yaml

echo ""
echo "⚙️ Deploying services..."
kubectl apply -f manifests/services.yaml

echo ""
echo "🔧 Deploying engine DaemonSet..."
kubectl apply -f manifests/engine-daemonset.yaml

echo ""
echo "🌐 Deploying web interface..."
kubectl apply -f manifests/web-deployment.yaml

echo ""
echo "🚪 Deploying ingress..."
kubectl apply -f manifests/ingress.yaml

echo ""
echo "⏳ Waiting for deployments to be ready..."
kubectl wait --for=condition=available --timeout=120s deployment/gameoflife-controller -n gameoflife
kubectl wait --for=condition=available --timeout=120s deployment/gameoflife-web -n gameoflife

echo ""
echo "📊 Checking pod status..."
kubectl get pods -n gameoflife -o wide

echo ""
echo "🔍 Checking services..."
kubectl get services -n gameoflife

echo ""
echo "🌍 Checking ingress..."
kubectl get ingress -n gameoflife

echo ""
echo "🎉 Deployment complete!"
echo ""
echo "Access options:"
echo "  🌐 External: https://gameoflife.ticktockbent.com"
echo "  🏠 Local: http://gameoflife.local (add to /etc/hosts)"
echo "  🔧 Port-forward: kubectl port-forward svc/gameoflife-web 8080:80 -n gameoflife"
echo ""
echo "To check logs: kubectl logs -f deployment/gameoflife-controller -n gameoflife"
echo "To delete: kubectl delete namespace gameoflife"