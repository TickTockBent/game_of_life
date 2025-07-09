# Container Registry Usage Guide

Quick reference for using our internal Docker registry with Vault authentication.

## Registry Details

- **URL**: `192.168.68.100:5000`
- **Authentication**: Vault-managed htpasswd
- **Storage**: 2TB NFS-backed persistent volume
- **Access**: Internal lab network only

## Authentication

### Get Current Credentials

```bash
# Get registry credentials from Vault
export VAULT_ADDR='http://192.168.68.100:8200'
vault kv get docker-registry/creds

# Example output:
# username: registry
# password: qQ1Yq39vt42baVdXAr0MRs3O/glxua/aae7sfelPpv0=
```

### Docker Login

```bash
# Login to registry
docker login 192.168.68.100:5000
# Username: registry
# Password: [paste password from Vault]
```

## Basic Usage

### Pull Image

```bash
# Pull from registry
docker pull 192.168.68.100:5000/jellyfin:latest
```

### Push Image

```bash
# Tag existing image
docker tag jellyfin/jellyfin:latest 192.168.68.100:5000/jellyfin:latest

# Push to registry
docker push 192.168.68.100:5000/jellyfin:latest
```

### List Images

```bash
# List repositories
curl -u "registry:PASSWORD" http://192.168.68.100:5000/v2/_catalog

# List tags for a repository
curl -u "registry:PASSWORD" http://192.168.68.100:5000/v2/jellyfin/tags/list
```

## Kubernetes Integration

### Cluster-Wide Authentication

Kubernetes nodes are pre-configured with registry credentials in `/etc/rancher/k3s/registries.yaml`:

```yaml
mirrors:
  "192.168.68.100:5000":
    endpoint:
      - "http://192.168.68.100:5000"
configs:
  "192.168.68.100:5000":
    auth:
      username: registry
      password: qQ1Yq39vt42baVdXAr0MRs3O/glxua/aae7sfelPpv0=
    tls:
      insecure_skip_verify: true
```

### Using in Deployments

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  template:
    spec:
      containers:
      - name: my-app
        image: 192.168.68.100:5000/my-app:latest
        # No imagePullSecrets needed - cluster-wide auth configured
```

## Common Workflows

### Migrate External Image

```bash
# Pull from Docker Hub
docker pull nginx:latest

# Tag for internal registry
docker tag nginx:latest 192.168.68.100:5000/nginx:latest

# Push to internal registry
docker push 192.168.68.100:5000/nginx:latest

# Update Kubernetes deployment
kubectl patch deployment my-app -p '{"spec":{"template":{"spec":{"containers":[{"name":"my-app","image":"192.168.68.100:5000/nginx:latest"}]}}}}'
```

### Check Image Status

```bash
# Check if image exists in registry
curl -u "registry:PASSWORD" -I http://192.168.68.100:5000/v2/nginx/manifests/latest

# Get image digest
curl -u "registry:PASSWORD" -H "Accept: application/vnd.docker.distribution.manifest.v2+json" \
  http://192.168.68.100:5000/v2/nginx/manifests/latest | jq -r '.config.digest'
```

### Cleanup Old Images

```bash
# List all tags
curl -u "registry:PASSWORD" http://192.168.68.100:5000/v2/nginx/tags/list

# Delete specific tag (requires registry with delete enabled)
curl -u "registry:PASSWORD" -X DELETE http://192.168.68.100:5000/v2/nginx/manifests/DIGEST
```

## Registry Management

### Check Registry Health

```bash
# Health check
curl http://192.168.68.100:5000/

# Registry info
curl -u "registry:PASSWORD" http://192.168.68.100:5000/v2/
```

### View Registry Logs

```bash
# Check registry pod logs
kubectl logs -f deployment/docker-registry -c registry -n registry

# Check Vault agent logs
kubectl logs -f deployment/docker-registry -c vault-agent -n registry
```

### Restart Registry

```bash
# Restart registry deployment
kubectl rollout restart deployment/docker-registry -n registry

# Check rollout status
kubectl rollout status deployment/docker-registry -n registry
```

## Troubleshooting

### Authentication Issues

```bash
# Check if credentials are current
vault kv get docker-registry/creds

# Verify htpasswd file in registry
kubectl exec -n registry deployment/docker-registry -c registry -- cat /auth/htpasswd

# Test authentication
curl -u "registry:PASSWORD" http://192.168.68.100:5000/v2/
```

### Network Issues

```bash
# Test connectivity
curl -I http://192.168.68.100:5000/

# Check from k3s node
kubectl run test-pod --image=curlimages/curl --rm -it --restart=Never -- \
  curl -I http://192.168.68.100:5000/
```

### Storage Issues

```bash
# Check PVC status
kubectl get pvc -n registry

# Check storage usage
kubectl exec -n registry deployment/docker-registry -c registry -- \
  du -sh /var/lib/registry
```

## Security Notes

- **Internal Only**: Registry is not exposed externally
- **Authentication Required**: All operations require valid credentials
- **Vault Integration**: Credentials managed through Vault
- **Network Isolation**: Registry only accessible within lab network

## Backup Strategy

```bash
# Backup registry data (from registry pod)
kubectl exec -n registry deployment/docker-registry -c registry -- \
  tar -czf /tmp/registry-backup.tar.gz /var/lib/registry

# Copy backup locally
kubectl cp registry/POD_NAME:/tmp/registry-backup.tar.gz ./registry-backup.tar.gz
```

## Integration with Watchtower

The watchtower service automatically:
- Monitors upstream images for changes
- Pulls and pushes to internal registry
- Updates Kubernetes deployments
- Uses Vault credentials for authentication

See `watchtower/README.md` for more details on automated image management.

---

**Registry Status**: ✅ Operational with Vault authentication  
**Storage**: 2TB NFS-backed persistent volume  
**Authentication**: htpasswd with Vault credential management  
**Last Updated**: 2025-07-08