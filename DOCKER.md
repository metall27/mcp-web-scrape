# Docker Deployment Guide

Complete guide for deploying MCP Web Scrape Server using Docker.

## Quick Start

```bash
# Clone repository
git clone https://github.com/metall/mcp-web-scrape.git
cd mcp-web-scrape

# Run with docker-compose
docker-compose up -d

# Check status
docker-compose ps
docker-compose logs -f
```

## Architecture

### Multi-stage Build

Dockerfile uses multi-stage build for optimal image size:

**Stage 1 (builder):**
- Base: `golang:1.21-alpine`
- Purpose: Compile Go binary
- Size: ~300MB (temporary)

**Stage 2 (runtime):**
- Base: `alpine:latest`
- Purpose: Run application with Chrome
- Size: ~300MB (final)

### Image Size Breakdown

| Component | Size |
|-----------|------|
| Alpine Linux base | ~5MB |
| Chromium (headless) | ~150MB |
| Go binary (static) | ~20MB |
| CA certificates | ~2MB |
| Other utilities | ~3MB |
| **Total** | **~180MB** |

## Ports and Networking

### Default Ports

| Port | Protocol | Purpose | External |
|------|----------|---------|----------|
| 8192 | HTTP | MCP server & API | 8192 |

### Port Mapping

```yaml
ports:
  - "8192:8192"  # Host:Container
```

### Docker Network

```yaml
networks:
  mcp-network:
    driver: bridge
```

## Configuration

### Environment Variables

```yaml
environment:
  # Server
  - MCP_WEB_SCRAPE_SERVER_HOST=0.0.0.0
  - MCP_WEB_SCRAPE_SERVER_PORT=8192
  - MCP_WEB_SCRAPE_LOG_LEVEL=info

  # Browser
  - MCP_WEB_SCRAPE_BROWSER_ENABLED=true
  - MCP_WEB_SCRAPE_BROWSER_HEADLESS=true
  - MCP_WEB_SCRAPE_BROWSER_NO_SANDBOX=true

  # Performance
  - MCP_WEB_SCRAPE_RATE_LIMIT_ENABLED=true
  - MCP_WEB_SCRAPE_CACHE_ENABLED=true
```

### Volume Mounts

```yaml
volumes:
  # Configuration (read-only)
  - ./config.yaml:/app/config.yaml:ro

  # Cache directory (persistent)
  - mcp-cache:/app/cache
```

## Resource Management

### CPU & Memory Limits

```yaml
deploy:
  resources:
    limits:
      cpus: '2.0'      # Max 2 CPU cores
      memory: 2G       # Max 2GB RAM
    reservations:
      cpus: '0.5'      # Min 0.5 CPU core
      memory: 512M     # Min 512MB RAM
```

**Recommended for production:**
- Small load: 1 CPU, 1GB RAM
- Medium load: 2 CPUs, 2GB RAM
- Heavy load: 4 CPUs, 4GB RAM

### Chrome Resource Usage

Headless Chrome typically uses:
- RAM: 100-300MB per instance
- CPU: Spike during page load
- Disk: Minimal (no cache on disk)

## Health Checks

### Configuration

```yaml
healthcheck:
  test: ["CMD", "wget", "--spider", "-q", "http://localhost:8192/health"]
  interval: 30s      # Check every 30 seconds
  timeout: 3s        # Timeout after 3 seconds
  retries: 3         # 3 retries before unhealthy
  start_period: 10s  # Grace period on start
```

### Monitoring Health

```bash
# Check container health
docker ps
docker inspect mcp-web-scrape | grep -A 10 Health

# Manual health check
docker exec mcp-web-scrape wget -O- http://localhost:8192/health
```

## Deployment Scenarios

### Scenario 1: Single Host

```bash
docker-compose up -d
```

**Access:** `http://localhost:8192`

### Scenario 2: Custom Port

```yaml
ports:
  - "9090:8192"
```

**Access:** `http://localhost:9090`

### Scenario 3: Reverse Proxy (Nginx)

**nginx.conf:**
```nginx
upstream mcp_backend {
    server localhost:8192;
}

server {
    listen 80;
    server_name mcp.example.com;

    location /mcp {
        proxy_pass http://mcp_backend;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # SSE support
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_buffering off;
        proxy_cache off;
    }
}
```

### Scenario 4: Docker Swarm

```bash
# Initialize swarm
docker swarm init

# Deploy stack
docker stack deploy -c docker-compose.yml mcp-stack

# Scale services
docker service scale mcp-stack_mcp-web-scrape=3
```

### Scenario 5: Kubernetes

```yaml
# deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mcp-web-scrape
spec:
  replicas: 3
  selector:
    matchLabels:
      app: mcp-web-scrape
  template:
    metadata:
      labels:
        app: mcp-web-scrape
    spec:
      containers:
      - name: mcp-web-scrape
        image: mcp-web-scrape:latest
        ports:
        - containerPort: 8192
        resources:
          requests:
            memory: "512Mi"
            cpu: "500m"
          limits:
            memory: "2Gi"
            cpu: "2000m"
        livenessProbe:
          httpGet:
            path: /health
            port: 8192
          initialDelaySeconds: 10
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /health
            port: 8192
          initialDelaySeconds: 5
          periodSeconds: 10
---
apiVersion: v1
kind: Service
metadata:
  name: mcp-web-scrape
spec:
  selector:
    app: mcp-web-scrape
  ports:
  - port: 8192
    targetPort: 8192
  type: LoadBalancer
```

## Troubleshooting

### Issue: Container exits immediately

```bash
# Check logs
docker-compose logs mcp-web-scrape

# Common causes:
# 1. Port already in use
# 2. Permission issues
# 3. Invalid configuration
```

**Solution:**
```bash
# Check port usage
netstat -tlnp | grep 8192

# Change port in docker-compose.yml
ports:
  - "9090:8192"
```

### Issue: Chrome crashes

```bash
# Check logs for Chrome errors
docker-compose logs | grep -i chrome
```

**Solution:**
```yaml
# Add Chrome flags
environment:
  - MCP_WEB_SCRAPE_BROWSER_NO_SANDBOX=true
  - MCP_WEB_SCRAPE_BROWSER_DISABLE_GPU=true
```

### Issue: Out of memory

```bash
# Check container stats
docker stats mcp-web-scrape
```

**Solution:**
```yaml
# Increase memory limit
deploy:
  resources:
    limits:
      memory: 4G
```

### Issue: Slow performance

**Solutions:**
1. Enable image blocking:
```json
{
  "name": "scrape_with_js",
  "arguments": {
    "url": "https://example.com",
    "block_images": true
  }
}
```

2. Reduce concurrent Chrome instances
3. Add more CPU cores

### Issue: Health check failing

```bash
# Manual health check
docker exec mcp-web-scrape wget -O- http://localhost:8192/health

# Check if service is listening
docker exec mcp-web-scrape netstat -tlnp
```

## Security

### Non-root User

Container runs as non-root user (`mcp:1000`):

```dockerfile
RUN addgroup -g 1000 -S mcp && \
    adduser -u 1000 -S mcp -G mcp
USER mcp
```

### Read-only Filesystem

```yaml
# Add to docker-compose.yml
read_only: true
tmpfs:
  - /tmp
```

### Secrets Management

```bash
# Use Docker secrets
echo "your_api_key" | docker secret create brave_api_key -

# Add to docker-compose.yml
secrets:
  brave_api_key:
    external: true

environment:
  - BRAVE_API_KEY_FILE=/run/secrets/brave_api_key
```

## Monitoring

### Logs

```bash
# Follow logs
docker-compose logs -f

# Last 100 lines
docker-compose logs --tail=100

# Specific service
docker-compose logs -f mcp-web-scrape
```

### Metrics

```bash
# Check metrics endpoint
curl http://localhost:8192/metrics

# Response:
{
  "rate_limit": {
    "requests_per_second": 10.0,
    "burst_size": 20
  },
  "cache": {
    "enabled": true
  }
}
```

### Performance Monitoring

```bash
# Container stats
docker stats mcp-web-scrape

# Detailed inspection
docker inspect mcp-web-scrape
```

## Backup and Restore

### Backup Configuration

```bash
# Backup config and cache
tar czf mcp-backup-$(date +%Y%m%d).tar.gz \
  config.yaml \
  docker-compose.yml \
  /var/lib/docker/volumes/mcp-web-scrape_mcp-cache/_data
```

### Restore

```bash
# Extract backup
tar xzf mcp-backup-20240430.tar.gz

# Restore volumes
docker volume rm mcp-cache
docker volume create mcp-cache
# ... restore data
```

## Updates

### Update Image

```bash
# Pull latest changes
git pull

# Rebuild image
docker-compose build

# Restart with new image
docker-compose up -d

# Remove old images
docker image prune -a
```

### Rolling Update (Zero Downtime)

```bash
# Deploy new version
docker-compose up -d --no-deps --build mcp-web-scrape

# Wait for health check
sleep 10

# Verify
docker-compose ps
curl http://localhost:8192/health
```

## Production Checklist

- [ ] Use Docker secrets for API keys
- [ ] Configure resource limits
- [ ] Enable health checks
- [ ] Set up log rotation
- [ ] Configure backup strategy
- [ ] Use reverse proxy (nginx/traefik)
- [ ] Enable HTTPS
- [ ] Set up monitoring (Prometheus/Grafana)
- [ ] Configure alerting
- [ ] Test disaster recovery

## Performance Tips

1. **Use image blocking** for faster scraping
2. **Adjust rate limits** based on load
3. **Enable caching** for repeated requests
4. **Use multiple instances** behind load balancer
5. **Monitor Chrome processes** to prevent memory leaks

## Support

For issues and questions:
- GitHub: https://github.com/metall/mcp-web-scrape/issues
- Documentation: See AGENTS.md for technical details
