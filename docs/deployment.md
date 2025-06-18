# Go-MLS Deployment Guide

This guide covers deployment strategies, infrastructure requirements, and best practices for running Go-MLS in production environments.

---

## Deployment Options

### 1. Direct Binary Deployment

The simplest deployment method for small to medium installations.

#### System Requirements
- **CPU**: 2+ cores (4+ recommended for multiple streams)
- **RAM**: 2GB minimum (4GB+ recommended)
- **Storage**: 10GB+ for application and recordings
- **Network**: 100Mbps+ for multiple high-quality streams
- **OS**: Linux (Ubuntu 20.04+, CentOS 7+, Debian 10+)

#### Installation Steps

```bash
# Create dedicated user
sudo useradd -r -s /bin/false go-mls

# Create directories
sudo mkdir -p /opt/go-mls/{bin,config,recordings,logs}
sudo chown -R go-mls:go-mls /opt/go-mls

# Download and install
wget https://github.com/krsna/go-mls/releases/latest/download/go-mls-linux-amd64.tar.gz
tar xzf go-mls-linux-amd64.tar.gz
sudo cp go-mls /opt/go-mls/bin/
sudo chmod +x /opt/go-mls/bin/go-mls

# Install ffmpeg
sudo apt update
sudo apt install -y ffmpeg
```

#### Systemd Service

Create `/etc/systemd/system/go-mls.service`:

```ini
[Unit]
Description=Go Media Live Streamer
After=network.target
Wants=network.target

[Service]
Type=simple
User=go-mls
Group=go-mls
WorkingDirectory=/opt/go-mls
ExecStart=/opt/go-mls/bin/go-mls -config /opt/go-mls/config/relay_config.json
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=go-mls

# Security settings
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/go-mls/recordings /opt/go-mls/logs

# Resource limits
LimitNOFILE=65536
LimitNPROC=32768

# Environment
Environment=GO_MLS_RECORDINGS_DIR=/opt/go-mls/recordings
Environment=GO_MLS_LOG_LEVEL=info

[Install]
WantedBy=multi-user.target
```

#### Start and Enable Service

```bash
sudo systemctl daemon-reload
sudo systemctl enable go-mls
sudo systemctl start go-mls
sudo systemctl status go-mls
```

---

### 2. Docker Deployment

Containerized deployment for better isolation and easier management.

#### Dockerfile

```dockerfile
FROM golang:1.23-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o go-mls .

FROM alpine:latest

# Install ffmpeg and ca-certificates
RUN apk --no-cache add ffmpeg ca-certificates tzdata

# Create non-root user
RUN adduser -D -s /bin/sh go-mls

# Create directories
RUN mkdir -p /app/recordings /app/config /app/logs
RUN chown -R go-mls:go-mls /app

# Copy binary and web assets
COPY --from=builder /app/go-mls /app/
COPY --from=builder /app/web/ /app/web/

USER go-mls
WORKDIR /app

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/api/health || exit 1

EXPOSE 8080 8554

CMD ["./go-mls"]
```

#### Build and Run

```bash
# Build image
docker build -t go-mls:latest .

# Run container
docker run -d \
  --name go-mls \
  -p 8080:8080 \
  -p 8554:8554 \
  -v $(pwd)/recordings:/app/recordings \
  -v $(pwd)/config:/app/config \
  -e GO_MLS_LOG_LEVEL=info \
  go-mls:latest
```

---

### 3. Docker Compose Deployment

Complete stack with monitoring and persistence.

#### docker-compose.yml

```yaml
version: '3.8'

services:
  go-mls:
    build: .
    container_name: go-mls
    restart: unless-stopped
    ports:
      - "8080:8080"
      - "8554:8554"
    environment:
      - GO_MLS_PORT=8080
      - GO_MLS_HOST=0.0.0.0
      - GO_MLS_LOG_LEVEL=info
      - GO_MLS_RECORDINGS_DIR=/app/recordings
      - GO_MLS_RTSP_PORT=8554
    volumes:
      - recordings_data:/app/recordings
      - config_data:/app/config
      - logs_data:/app/logs
    networks:
      - go-mls-network
    healthcheck:
      test: ["CMD", "wget", "--no-verbose", "--tries=1", "--spider", "http://localhost:8080/api/health"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 40s

  prometheus:
    image: prom/prometheus:latest
    container_name: go-mls-prometheus
    restart: unless-stopped
    ports:
      - "9090:9090"
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml:ro
      - prometheus_data:/prometheus
    command:
      - '--config.file=/etc/prometheus/prometheus.yml'
      - '--storage.tsdb.path=/prometheus'
      - '--web.console.libraries=/etc/prometheus/console_libraries'
      - '--web.console.templates=/etc/prometheus/consoles'
      - '--storage.tsdb.retention.time=15d'
      - '--web.enable-lifecycle'
    networks:
      - go-mls-network

  grafana:
    image: grafana/grafana:latest
    container_name: go-mls-grafana
    restart: unless-stopped
    ports:
      - "3000:3000"
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=admin
      - GF_USERS_ALLOW_SIGN_UP=false
    volumes:
      - grafana_data:/var/lib/grafana
      - ./provisioning:/etc/grafana/provisioning
    networks:
      - go-mls-network

  nginx:
    image: nginx:alpine
    container_name: go-mls-nginx
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./nginx.conf:/etc/nginx/nginx.conf:ro
      - ./ssl:/etc/nginx/ssl:ro
    depends_on:
      - go-mls
    networks:
      - go-mls-network

volumes:
  recordings_data:
  config_data:
  logs_data:
  prometheus_data:
  grafana_data:

networks:
  go-mls-network:
    driver: bridge
```

#### Supporting Configuration Files

**prometheus.yml:**
```yaml
global:
  scrape_interval: 15s
  evaluation_interval: 15s

scrape_configs:
  - job_name: 'go-mls'
    static_configs:
      - targets: ['go-mls:8080']
    metrics_path: '/metrics'
    scrape_interval: 10s
```

**nginx.conf:**
```nginx
events {
    worker_connections 1024;
}

http {
    upstream go-mls {
        server go-mls:8080;
    }

    server {
        listen 80;
        server_name your-domain.com;

        # Increase body size for config uploads
        client_max_body_size 100M;

        location / {
            proxy_pass http://go-mls;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
            
            # WebSocket support for real-time logs
            proxy_http_version 1.1;
            proxy_set_header Upgrade $http_upgrade;
            proxy_set_header Connection "upgrade";
        }

        # Health check endpoint
        location /health {
            access_log off;
            proxy_pass http://go-mls/api/health;
        }
    }
}
```

---

### 4. Kubernetes Deployment

Enterprise-grade deployment with auto-scaling and high availability.

#### Namespace and ConfigMap

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: go-mls
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: go-mls-config
  namespace: go-mls
data:
  relay_config.json: |
    {
      "inputs": [],
      "endpoints": []
    }
```

#### Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: go-mls
  namespace: go-mls
  labels:
    app: go-mls
spec:
  replicas: 3
  selector:
    matchLabels:
      app: go-mls
  template:
    metadata:
      labels:
        app: go-mls
    spec:
      containers:
      - name: go-mls
        image: go-mls:latest
        ports:
        - containerPort: 8080
          name: http
        - containerPort: 8554
          name: rtsp
        env:
        - name: GO_MLS_PORT
          value: "8080"
        - name: GO_MLS_HOST
          value: "0.0.0.0"
        - name: GO_MLS_LOG_LEVEL
          value: "info"
        - name: GO_MLS_RECORDINGS_DIR
          value: "/recordings"
        volumeMounts:
        - name: recordings
          mountPath: /recordings
        - name: config
          mountPath: /config
        resources:
          requests:
            memory: "2Gi"
            cpu: "1000m"
          limits:
            memory: "4Gi"
            cpu: "2000m"
        livenessProbe:
          httpGet:
            path: /api/health
            port: 8080
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /api/health
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5
      volumes:
      - name: recordings
        persistentVolumeClaim:
          claimName: go-mls-recordings
      - name: config
        configMap:
          name: go-mls-config
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        fsGroup: 1000
```

#### Services

```yaml
apiVersion: v1
kind: Service
metadata:
  name: go-mls-service
  namespace: go-mls
spec:
  selector:
    app: go-mls
  ports:
  - name: http
    port: 8080
    targetPort: 8080
  - name: rtsp
    port: 8554
    targetPort: 8554
  type: ClusterIP
---
apiVersion: v1
kind: Service
metadata:
  name: go-mls-rtsp-lb
  namespace: go-mls
spec:
  selector:
    app: go-mls
  ports:
  - name: rtsp
    port: 8554
    targetPort: 8554
  type: LoadBalancer
```

#### Ingress

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: go-mls-ingress
  namespace: go-mls
  annotations:
    nginx.ingress.kubernetes.io/proxy-body-size: "100m"
    nginx.ingress.kubernetes.io/proxy-read-timeout: "300"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "300"
spec:
  ingressClassName: nginx
  tls:
  - hosts:
    - go-mls.your-domain.com
    secretName: go-mls-tls
  rules:
  - host: go-mls.your-domain.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: go-mls-service
            port:
              number: 8080
```

#### Persistent Volume Claims

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: go-mls-recordings
  namespace: go-mls
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 500Gi
  storageClassName: fast-ssd
```

#### HorizontalPodAutoscaler

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: go-mls-hpa
  namespace: go-mls
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: go-mls
  minReplicas: 3
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
  - type: Resource
    resource:
      name: memory
      target:
        type: Utilization
        averageUtilization: 80
```

---

## Production Best Practices

### 1. Security Hardening

#### Network Security
```bash
# Firewall rules (UFW example)
sudo ufw allow 22/tcp    # SSH
sudo ufw allow 8080/tcp  # HTTP API
sudo ufw allow 8554/tcp  # RTSP
sudo ufw deny 8554/tcp from any to any port 8554  # Restrict RTSP to internal
sudo ufw enable
```

#### Application Security
```bash
# Run as non-root user
sudo useradd -r -s /bin/false go-mls

# Set proper file permissions
sudo chmod 750 /opt/go-mls/bin/go-mls
sudo chmod 640 /opt/go-mls/config/relay_config.json
sudo chmod 755 /opt/go-mls/recordings
```

#### TLS Configuration
```nginx
server {
    listen 443 ssl http2;
    ssl_certificate /etc/ssl/certs/go-mls.crt;
    ssl_certificate_key /etc/ssl/private/go-mls.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers ECDHE-RSA-AES256-GCM-SHA512:DHE-RSA-AES256-GCM-SHA512;
    ssl_prefer_server_ciphers off;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 10m;
}
```

### 2. Performance Optimization

#### System Tuning
```bash
# Increase file descriptor limits
echo "go-mls soft nofile 65536" >> /etc/security/limits.conf
echo "go-mls hard nofile 65536" >> /etc/security/limits.conf

# Network tuning
echo "net.core.rmem_max = 134217728" >> /etc/sysctl.conf
echo "net.core.wmem_max = 134217728" >> /etc/sysctl.conf
echo "net.ipv4.tcp_rmem = 4096 65536 134217728" >> /etc/sysctl.conf
echo "net.ipv4.tcp_wmem = 4096 65536 134217728" >> /etc/sysctl.conf
sysctl -p
```

#### Go Runtime Tuning
```bash
# Environment variables for performance
export GOMAXPROCS=8
export GOGC=30
export GODEBUG=gctrace=1  # For debugging only
```

### 3. Monitoring and Alerting

#### Prometheus Alerts
```yaml
groups:
- name: go-mls
  rules:
  - alert: GoMLSDown
    expr: up{job="go-mls"} == 0
    for: 1m
    labels:
      severity: critical
    annotations:
      summary: "Go-MLS instance is down"

  - alert: HighCPUUsage
    expr: rate(process_cpu_seconds_total{job="go-mls"}[5m]) * 100 > 80
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "High CPU usage detected"

  - alert: StreamFailure
    expr: go_mls_failed_streams_total > 0
    for: 1m
    labels:
      severity: warning
    annotations:
      summary: "Stream failures detected"
```

### 4. Backup and Recovery

#### Configuration Backup
```bash
#!/bin/bash
# backup-config.sh

BACKUP_DIR="/backup/go-mls/$(date +%Y%m%d_%H%M%S)"
mkdir -p "$BACKUP_DIR"

# Backup configuration
curl -s http://localhost:8080/api/export-config > "$BACKUP_DIR/relay_config.json"

# Backup system configuration
cp /etc/systemd/system/go-mls.service "$BACKUP_DIR/"
cp /opt/go-mls/config/* "$BACKUP_DIR/"

# Create archive
tar czf "$BACKUP_DIR.tar.gz" -C /backup/go-mls/ "$(basename $BACKUP_DIR)"
rm -rf "$BACKUP_DIR"
```

#### Recording Backup
```bash
#!/bin/bash
# backup-recordings.sh

RECORDINGS_DIR="/opt/go-mls/recordings"
BACKUP_DEST="s3://backup-bucket/go-mls/recordings/"

# Sync recordings to S3
aws s3 sync "$RECORDINGS_DIR" "$BACKUP_DEST" --delete

# Clean up old local recordings (older than 7 days)
find "$RECORDINGS_DIR" -name "*.mp4" -mtime +7 -delete
```

### 5. Disaster Recovery

#### Recovery Procedure
```bash
#!/bin/bash
# disaster-recovery.sh

# 1. Restore from backup
BACKUP_FILE="/backup/go-mls/latest.tar.gz"
tar xzf "$BACKUP_FILE" -C /tmp/

# 2. Restore configuration
sudo systemctl stop go-mls
cp /tmp/relay_config.json /opt/go-mls/config/
cp /tmp/go-mls.service /etc/systemd/system/

# 3. Reload and restart
sudo systemctl daemon-reload
sudo systemctl start go-mls

# 4. Import configuration
curl -X POST http://localhost:8080/api/import-config \
  -H "Content-Type: application/json" \
  -d @/opt/go-mls/config/relay_config.json

echo "Recovery completed"
```

---

## Scaling Strategies

### 1. Vertical Scaling

**Hardware Scaling Guidelines:**
- **Light Load (1-10 streams)**: 2 CPU cores, 4GB RAM
- **Medium Load (10-50 streams)**: 4-8 CPU cores, 8-16GB RAM  
- **Heavy Load (50+ streams)**: 8+ CPU cores, 16+ GB RAM

### 2. Horizontal Scaling

**Load Balancing Strategy:**
```nginx
upstream go-mls-cluster {
    least_conn;
    server go-mls-1:8080 weight=1;
    server go-mls-2:8080 weight=1;
    server go-mls-3:8080 weight=1;
}

server {
    location / {
        proxy_pass http://go-mls-cluster;
    }
}
```

### 3. Geographic Distribution

**Multi-Region Deployment:**
- Deploy instances in multiple regions
- Use anycast or GeoDNS for routing
- Replicate configurations across regions
- Monitor cross-region latency

---

## Troubleshooting Production Issues

### Common Issues and Solutions

#### 1. High Memory Usage
```bash
# Check memory usage
ps aux | grep go-mls
top -p $(pgrep go-mls)

# Solutions:
# - Reduce GOGC value
# - Limit concurrent streams
# - Increase system memory
```

#### 2. FFmpeg Process Leaks
```bash
# Check for orphaned processes
ps aux | grep ffmpeg | grep -v grep

# Kill orphaned processes
pkill -f "ffmpeg.*rtmp"

# Prevention: Ensure proper process cleanup in code
```

#### 3. Stream Quality Issues
```bash
# Check network bandwidth
iftop -i eth0

# Monitor ffmpeg logs
journalctl -u go-mls -f | grep ffmpeg

# Adjust bitrate and quality settings
```

### Health Checks

#### System Health Script
```bash
#!/bin/bash
# health-check.sh

# Check service status
systemctl is-active go-mls || exit 1

# Check API endpoint
curl -f http://localhost:8080/api/health || exit 1

# Check disk space
DISK_USAGE=$(df /opt/go-mls/recordings | tail -1 | awk '{print $5}' | sed 's/%//')
if [ "$DISK_USAGE" -gt 90 ]; then
    echo "Disk usage too high: ${DISK_USAGE}%"
    exit 1
fi

echo "All checks passed"
```

This deployment guide provides comprehensive instructions for deploying Go-MLS in various environments, from development to enterprise production.
