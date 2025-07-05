# Docker Registry with Vault Authentication

## Overview

This document describes the Vault-integrated Docker registry deployment that provides centralized credential management and automatic credential rotation capabilities.

## Architecture

The Docker registry runs with a Vault Agent sidecar that:
- Authenticates to Vault using a service token
- Retrieves credentials from Vault KV store
- Generates htpasswd files automatically
- Watches for credential changes and updates the registry in real-time

## Components

### Vault Configuration
- **Secrets Engine**: `docker-registry/` (KV v2)
- **Policy**: `docker-registry` (read access to credentials)
- **Secret Path**: `docker-registry/creds`

### Kubernetes Resources
- **Namespace**: `registry`
- **Deployment**: `docker-registry` (with Vault Agent sidecar)
- **Service**: `docker-registry` (ClusterIP)
- **Ingress**: `registry-traefik-ingress` (Traefik with Let's Encrypt TLS)
- **PVC**: `registry-pvc` (10Gi storage)

## Usage

### Authentication

#### Option 1: Use the Helper Script
```bash
# Login to the registry using Vault credentials
/home/ticktockbent/RemoteDev/docker-registry-login.sh
```

#### Option 2: Manual Login
```bash
# Set Vault address
export VAULT_ADDR='http://127.0.0.1:8200'

# Get credentials from Vault
CREDS=$(vault kv get -format=json docker-registry/creds)
USERNAME=$(echo $CREDS | jq -r '.data.data.username')
PASSWORD=$(echo $CREDS | jq -r '.data.data.password')

# Login to Docker registry
echo $PASSWORD | docker login registry.ticktockbent.com -u $USERNAME --password-stdin
```

### Push/Pull Images
```bash
# Tag your image
docker tag myapp:latest registry.ticktockbent.com/myapp:latest

# Push to registry
docker push registry.ticktockbent.com/myapp:latest

# Pull from registry
docker pull registry.ticktockbent.com/myapp:latest
```

## Integration with Deployments

### CI/CD Pipeline Integration

#### Option 1: Vault Agent in CI
```yaml
# .github/workflows/deploy.yml
- name: Login to Registry
  env:
    VAULT_ADDR: http://vault.internal:8200
    VAULT_TOKEN: ${{ secrets.VAULT_TOKEN }}
  run: |
    CREDS=$(vault kv get -format=json docker-registry/creds)
    USERNAME=$(echo $CREDS | jq -r '.data.data.username')
    PASSWORD=$(echo $CREDS | jq -r '.data.data.password')
    echo $PASSWORD | docker login registry.ticktockbent.com -u $USERNAME --password-stdin
```

#### Option 2: Kubernetes ImagePullSecrets
Create a secret in your deployment namespace:
```bash
# Get credentials from Vault
export VAULT_ADDR='http://127.0.0.1:8200'
CREDS=$(vault kv get -format=json docker-registry/creds)
USERNAME=$(echo $CREDS | jq -r '.data.data.username')
PASSWORD=$(echo $CREDS | jq -r '.data.data.password')

# Create Docker registry secret
kubectl create secret docker-registry registry-secret \
  --docker-server=registry.ticktockbent.com \
  --docker-username=$USERNAME \
  --docker-password=$PASSWORD \
  --docker-email=admin@ticktockbent.com \
  -n your-namespace
```

Use in your deployment:
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: myapp
spec:
  template:
    spec:
      imagePullSecrets:
      - name: registry-secret
      containers:
      - name: myapp
        image: registry.ticktockbent.com/myapp:latest
```

### Vault Agent Sidecar Pattern

For applications that need frequent registry access, deploy a Vault Agent sidecar:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: myapp-with-vault
spec:
  template:
    spec:
      containers:
      - name: vault-agent
        image: hashicorp/vault:1.14.0
        command: ['vault', 'agent', '-config=/vault/config/agent.hcl']
        env:
        - name: SKIP_SETCAP
          value: "true"
        volumeMounts:
        - name: vault-config
          mountPath: /vault/config
        - name: vault-token
          mountPath: /vault/token
        - name: docker-config
          mountPath: /root/.docker
      - name: myapp
        image: registry.ticktockbent.com/myapp:latest
        volumeMounts:
        - name: docker-config
          mountPath: /root/.docker
      volumes:
      - name: vault-config
        configMap:
          name: vault-docker-config
      - name: vault-token
        secret:
          secretName: vault-token
      - name: docker-config
        emptyDir: {}
```

## Credential Management

### View Current Credentials
```bash
export VAULT_ADDR='http://127.0.0.1:8200'
vault kv get docker-registry/creds
```

### Rotate Credentials
```bash
export VAULT_ADDR='http://127.0.0.1:8200'

# Generate new password
NEW_PASSWORD=$(openssl rand -base64 32)

# Generate bcrypt hash
NEW_HASH=$(echo -n $NEW_PASSWORD | htpasswd -niB dummy | cut -d: -f2)

# Update in Vault
vault kv put docker-registry/creds \
  username=registry \
  password=$NEW_PASSWORD \
  password_hash=$NEW_HASH

# Vault Agent will automatically update the registry within seconds
```

### Add Additional Users
```bash
export VAULT_ADDR='http://127.0.0.1:8200'

# Store additional user credentials
vault kv put docker-registry/users/developer \
  username=developer \
  password=$(openssl rand -base64 32)

# Update registry to support multiple users (requires config change)
```

## Monitoring and Troubleshooting

### Check Registry Status
```bash
# Check pods
kubectl get pods -n registry

# Check logs
kubectl logs -n registry deployment/docker-registry -c registry
kubectl logs -n registry deployment/docker-registry -c vault-agent

# Check generated htpasswd file
kubectl exec -n registry deployment/docker-registry -c registry -- cat /auth/htpasswd
```

### Common Issues

#### Authentication Failures
1. Verify Vault is accessible: `vault status`
2. Check token validity: `vault token lookup`
3. Verify secret exists: `vault kv get docker-registry/creds`

#### Vault Agent Issues
1. Check agent logs: `kubectl logs -n registry deployment/docker-registry -c vault-agent`
2. Verify token secret: `kubectl get secret vault-token -n registry`
3. Check ConfigMap: `kubectl get configmap vault-agent-config -n registry`

#### Registry Connection Issues
1. Verify ingress: `kubectl get ingress -n registry`
2. Check TLS certificate: `curl -kv https://registry.ticktockbent.com/v2/`
3. Test internal access: `kubectl exec -it pod -- curl http://docker-registry:5000/v2/`

## Security Considerations

1. **Vault Token Rotation**: The service token should be rotated regularly
2. **Network Policies**: Consider restricting network access to the registry
3. **RBAC**: Limit Vault access to necessary services only
4. **Audit Logging**: Enable Vault audit logging for access tracking
5. **TLS**: Always use TLS for external registry access

## Backup and Recovery

### Backup Registry Data
```bash
# Backup persistent volume
kubectl exec -n registry deployment/docker-registry -c registry -- \
  tar czf - /var/lib/registry | gzip > registry-backup-$(date +%Y%m%d).tar.gz
```

### Backup Vault Secrets
```bash
# Export registry secrets
vault kv get -format=json docker-registry/creds > registry-secrets-backup.json
```

### Recovery
1. Restore persistent volume data
2. Restore Vault secrets
3. Redeploy the registry with Vault Agent

## Advanced Configuration

### Custom Vault Agent Template
Modify the htpasswd template for multiple users:

```hcl
{{ range secrets "docker-registry/users" }}
{{ with secret (printf "docker-registry/users/%s" .) }}
{{ .Data.data.username }}:{{ .Data.data.password_hash }}
{{ end }}
{{ end }}
```

### High Availability
- Deploy multiple registry replicas with shared storage
- Use external Vault cluster for production
- Implement registry caching/proxy for performance

## Files Reference

- **Deployment**: `/home/ticktockbent/RemoteDev/registry-with-vault.yaml`
- **Ingress**: `/home/ticktockbent/RemoteDev/registry-traefik-ingress.yaml`
- **Login Script**: `/home/ticktockbent/RemoteDev/docker-registry-login.sh`
- **Vault Policy**: `/home/ticktockbent/RemoteDev/vault-registry-policy.hcl`