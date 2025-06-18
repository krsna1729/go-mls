# Go-MLS Monitoring and Observability Guide

This guide covers monitoring, metrics, logging, and observability best practices for Go-MLS deployments.

---

## Monitoring Architecture

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│    Go-MLS       │───▶│   Prometheus    │───▶│    Grafana      │
│   /metrics      │    │   (Metrics)     │    │  (Dashboard)    │
└─────────────────┘    └─────────────────┘    └─────────────────┘
         │                       │                       │
         ▼                       ▼                       ▼
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Application   │    │   Time Series   │    │ Visualizations  │
│     Logs        │    │    Database     │    │   & Alerts      │
└─────────────────┘    └─────────────────┘    └─────────────────┘
         │
         ▼
┌─────────────────┐
│   ELK Stack     │
│ (Log Analysis)  │
└─────────────────┘
```

---

## Metrics Collection

### 1. Built-in Metrics Endpoint

Go-MLS exposes Prometheus-compatible metrics at `/metrics`:

```go
// Example metrics exposed by Go-MLS
go_mls_active_streams_total          // Number of active streams
go_mls_input_streams_total           // Number of input streams
go_mls_output_streams_total          // Number of output streams
go_mls_failed_streams_total          // Number of failed streams
go_mls_stream_bitrate_bytes_per_sec  // Current bitrate per stream
go_mls_ffmpeg_processes_total        // Active ffmpeg processes
go_mls_recordings_active_total       // Active recordings
go_mls_recordings_completed_total    // Completed recordings
go_mls_api_requests_total            // HTTP API requests
go_mls_api_request_duration_seconds  // HTTP request duration
```

### 2. System Metrics

Additional system-level metrics to monitor:

```yaml
# Prometheus node_exporter metrics
node_cpu_seconds_total               # CPU usage
node_memory_MemAvailable_bytes       # Available memory
node_filesystem_avail_bytes          # Disk space
node_network_receive_bytes_total     # Network I/O
node_load1                          # System load
```

### 3. Custom Metrics Implementation

Example of adding custom metrics to Go-MLS:

```go
package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    StreamsActive = promauto.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "go_mls_streams_active",
            Help: "Number of active streams by type",
        },
        []string{"type", "status"},
    )
    
    StreamBitrate = promauto.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "go_mls_stream_bitrate_bps",
            Help: "Current bitrate of streams in bits per second",
        },
        []string{"input_name", "output_name"},
    )
    
    FFmpegProcessDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "go_mls_ffmpeg_process_duration_seconds",
            Help: "Duration of ffmpeg processes",
            Buckets: prometheus.DefBuckets,
        },
        []string{"type", "status"},
    )
)
```

---

## Prometheus Configuration

### 1. Basic Prometheus Setup

**prometheus.yml:**
```yaml
global:
  scrape_interval: 15s
  evaluation_interval: 15s

rule_files:
  - "go-mls-rules.yml"

scrape_configs:
  - job_name: 'go-mls'
    static_configs:
      - targets: ['localhost:8080']
    metrics_path: '/metrics'
    scrape_interval: 10s
    scrape_timeout: 5s

  - job_name: 'node'
    static_configs:
      - targets: ['localhost:9100']
    scrape_interval: 15s

alerting:
  alertmanagers:
    - static_configs:
        - targets:
          - alertmanager:9093
```

### 2. Go-MLS Alert Rules

**go-mls-rules.yml:**
```yaml
groups:
- name: go-mls-alerts
  rules:
  
  # Service availability
  - alert: GoMLSDown
    expr: up{job="go-mls"} == 0
    for: 1m
    labels:
      severity: critical
    annotations:
      summary: "Go-MLS service is down"
      description: "Go-MLS has been down for more than 1 minute"

  # High error rate
  - alert: GoMLSHighErrorRate
    expr: |
      (
        rate(go_mls_api_requests_total{status=~"5.."}[5m]) /
        rate(go_mls_api_requests_total[5m])
      ) > 0.1
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "High error rate in Go-MLS API"
      description: "Error rate is {{ $value | humanizePercentage }}"

  # Stream failures
  - alert: GoMLSStreamFailures
    expr: increase(go_mls_failed_streams_total[5m]) > 0
    for: 1m
    labels:
      severity: warning
    annotations:
      summary: "Stream failures detected"
      description: "{{ $value }} stream failures in the last 5 minutes"

  # High CPU usage
  - alert: GoMLSHighCPU
    expr: |
      rate(process_cpu_seconds_total{job="go-mls"}[5m]) * 100 > 80
    for: 10m
    labels:
      severity: warning
    annotations:
      summary: "High CPU usage in Go-MLS"
      description: "CPU usage is {{ $value | humanize }}%"

  # High memory usage
  - alert: GoMLSHighMemory
    expr: |
      process_resident_memory_bytes{job="go-mls"} / 1024 / 1024 / 1024 > 4
    for: 15m
    labels:
      severity: warning
    annotations:
      summary: "High memory usage in Go-MLS"
      description: "Memory usage is {{ $value | humanize }}GB"

  # Low disk space
  - alert: GoMLSLowDiskSpace
    expr: |
      (
        node_filesystem_avail_bytes{mountpoint="/recordings"} /
        node_filesystem_size_bytes{mountpoint="/recordings"}
      ) < 0.1
    for: 5m
    labels:
      severity: critical
    annotations:
      summary: "Low disk space for recordings"
      description: "Only {{ $value | humanizePercentage }} disk space remaining"

  # Stream bitrate anomalies
  - alert: GoMLSLowBitrate
    expr: go_mls_stream_bitrate_bps < 1000000  # Less than 1 Mbps
    for: 2m
    labels:
      severity: warning
    annotations:
      summary: "Low bitrate detected"
      description: "Stream {{ $labels.input_name }}/{{ $labels.output_name }} has low bitrate: {{ $value | humanize }}bps"

  # Too many active streams
  - alert: GoMLSTooManyStreams
    expr: go_mls_streams_active > 100
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "High number of active streams"
      description: "{{ $value }} active streams detected"
```

---

## Grafana Dashboards

### 1. Main Dashboard Configuration

**go-mls-dashboard.json:**
```json
{
  "dashboard": {
    "id": null,
    "title": "Go-MLS Monitoring Dashboard",
    "tags": ["go-mls", "streaming"],
    "timezone": "browser",
    "panels": [
      {
        "id": 1,
        "title": "Service Status",
        "type": "stat",
        "targets": [
          {
            "expr": "up{job=\"go-mls\"}",
            "legendFormat": "Service Up"
          }
        ],
        "fieldConfig": {
          "defaults": {
            "color": {
              "mode": "thresholds"
            },
            "thresholds": {
              "steps": [
                {"color": "red", "value": 0},
                {"color": "green", "value": 1}
              ]
            }
          }
        }
      },
      {
        "id": 2,
        "title": "Active Streams",
        "type": "stat",
        "targets": [
          {
            "expr": "go_mls_streams_active",
            "legendFormat": "Active Streams"
          }
        ]
      },
      {
        "id": 3,
        "title": "API Request Rate",
        "type": "graph",
        "targets": [
          {
            "expr": "rate(go_mls_api_requests_total[5m])",
            "legendFormat": "Requests/sec"
          }
        ]
      },
      {
        "id": 4,
        "title": "Stream Bitrates",
        "type": "graph",
        "targets": [
          {
            "expr": "go_mls_stream_bitrate_bps / 1000000",
            "legendFormat": "{{input_name}}/{{output_name}}"
          }
        ],
        "yAxes": [
          {
            "label": "Mbps"
          }
        ]
      },
      {
        "id": 5,
        "title": "System Resources",
        "type": "graph",
        "targets": [
          {
            "expr": "rate(process_cpu_seconds_total{job=\"go-mls\"}[5m]) * 100",
            "legendFormat": "CPU %"
          },
          {
            "expr": "process_resident_memory_bytes{job=\"go-mls\"} / 1024 / 1024",
            "legendFormat": "Memory MB"
          }
        ]
      },
      {
        "id": 6,
        "title": "Error Rate",
        "type": "graph",
        "targets": [
          {
            "expr": "rate(go_mls_api_requests_total{status=~\"5..\"}[5m])",
            "legendFormat": "5xx Errors/sec"
          },
          {
            "expr": "rate(go_mls_failed_streams_total[5m])",
            "legendFormat": "Stream Failures/sec"
          }
        ]
      }
    ],
    "time": {
      "from": "now-1h",
      "to": "now"
    },
    "refresh": "10s"
  }
}
```

### 2. Detailed Streaming Dashboard

**streaming-details-dashboard.json:**
```json
{
  "dashboard": {
    "title": "Go-MLS Streaming Details",
    "panels": [
      {
        "title": "Streams by Status",
        "type": "piechart",
        "targets": [
          {
            "expr": "go_mls_streams_active",
            "legendFormat": "{{status}}"
          }
        ]
      },
      {
        "title": "Input vs Output Streams",
        "type": "stat",
        "targets": [
          {
            "expr": "go_mls_input_streams_total",
            "legendFormat": "Inputs"
          },
          {
            "expr": "go_mls_output_streams_total",
            "legendFormat": "Outputs"
          }
        ]
      },
      {
        "title": "FFmpeg Process Count",
        "type": "graph",
        "targets": [
          {
            "expr": "go_mls_ffmpeg_processes_total",
            "legendFormat": "FFmpeg Processes"
          }
        ]
      },
      {
        "title": "Recording Status",
        "type": "table",
        "targets": [
          {
            "expr": "go_mls_recordings_active_total",
            "legendFormat": "Active"
          },
          {
            "expr": "go_mls_recordings_completed_total",
            "legendFormat": "Completed"
          }
        ]
      }
    ]
  }
}
```

---

## Logging Strategy

### 1. Log Levels and Format

Go-MLS uses structured logging with the following levels:

```go
// Log levels
DEBUG   // Detailed operation information
INFO    // General operation status
WARN    // Warning conditions
ERROR   // Error conditions
FATAL   // Critical errors causing shutdown
```

**Log Format:**
```
2025/06/18 14:30:00 [INFO] Relay started: input=MainCamera, output=YouTube, bitrate=5.8Mbps
2025/06/18 14:30:01 [DEBUG] ffmpeg process started: PID=12345, cmd=[ffmpeg -i rtmp://... -c:v libx264 ...]
2025/06/18 14:30:02 [WARN] High CPU usage detected: 85%
2025/06/18 14:30:03 [ERROR] Stream connection failed: rtmp://source.example.com/live/stream: connection timeout
```

### 2. Log Collection Configuration

#### Systemd Journal
```bash
# View logs
journalctl -u go-mls -f

# Export logs
journalctl -u go-mls --since "2025-06-18 00:00:00" --until "2025-06-18 23:59:59" > go-mls.log
```

#### Docker Logging
```yaml
# docker-compose.yml
services:
  go-mls:
    logging:
      driver: "json-file"
      options:
        max-size: "100m"
        max-file: "5"
        labels: "service=go-mls"
```

#### Kubernetes Logging
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: fluent-bit-config
data:
  fluent-bit.conf: |
    [INPUT]
        Name tail
        Path /var/log/containers/*go-mls*.log
        Parser docker
        Tag kube.*
        
    [OUTPUT]
        Name es
        Match kube.*
        Host elasticsearch
        Port 9200
        Index go-mls-logs
```

### 3. ELK Stack Integration

#### Elasticsearch Mapping
```json
{
  "mappings": {
    "properties": {
      "timestamp": {"type": "date"},
      "level": {"type": "keyword"},
      "message": {"type": "text"},
      "input_name": {"type": "keyword"},
      "output_name": {"type": "keyword"},
      "bitrate": {"type": "float"},
      "process_id": {"type": "integer"},
      "error_code": {"type": "keyword"}
    }
  }
}
```

#### Logstash Configuration
```ruby
# logstash.conf
input {
  beats {
    port => 5044
  }
}

filter {
  if [fields][service] == "go-mls" {
    grok {
      match => { 
        "message" => "%{TIMESTAMP_ISO8601:timestamp} \[%{WORD:level}\] %{GREEDYDATA:log_message}"
      }
    }
    
    date {
      match => [ "timestamp", "yyyy/MM/dd HH:mm:ss" ]
    }
    
    if "bitrate=" in [log_message] {
      grok {
        match => { 
          "log_message" => "bitrate=%{NUMBER:bitrate:float}"
        }
      }
    }
  }
}

output {
  elasticsearch {
    hosts => ["elasticsearch:9200"]
    index => "go-mls-logs-%{+YYYY.MM.dd}"
  }
}
```

---

## Performance Monitoring

### 1. Application Performance Metrics

```yaml
# Custom application metrics
go_mls_stream_startup_duration_seconds    # Time to start stream
go_mls_stream_processing_latency_seconds  # Stream processing latency
go_mls_api_response_time_seconds         # API response times
go_mls_memory_usage_bytes                # Memory consumption
go_mls_goroutines_total                  # Number of goroutines
```

### 2. FFmpeg Performance Monitoring

Monitor ffmpeg processes individually:

```bash
#!/bin/bash
# monitor-ffmpeg.sh

while true; do
  for pid in $(pgrep ffmpeg); do
    cpu=$(ps -p $pid -o %cpu --no-headers)
    mem=$(ps -p $pid -o %mem --no-headers)
    echo "FFmpeg PID $pid: CPU ${cpu}%, Memory ${mem}%"
  done
  sleep 10
done
```

### 3. Network Performance

```yaml
# Network metrics to monitor
node_network_transmit_bytes_total     # Outbound traffic
node_network_receive_bytes_total      # Inbound traffic
node_network_transmit_packets_total   # Packet counts
node_netstat_Tcp_RetransSegs         # TCP retransmissions
```

---

## Health Checks

### 1. Application Health Check

```go
// Health check endpoint implementation
func healthHandler(w http.ResponseWriter, r *http.Request) {
    health := struct {
        Status           string            `json:"status"`
        Timestamp        time.Time         `json:"timestamp"`
        Uptime          string            `json:"uptime"`
        ActiveStreams   int               `json:"active_streams"`
        FFmpegProcesses int               `json:"ffmpeg_processes"`
        DiskSpace       map[string]string `json:"disk_space"`
        Memory          map[string]string `json:"memory"`
    }{
        Status:    "healthy",
        Timestamp: time.Now(),
        Uptime:    time.Since(startTime).String(),
    }
    
    // Check component health
    if !checkRTSPServer() {
        health.Status = "unhealthy"
    }
    
    json.NewEncoder(w).Encode(health)
}
```

### 2. External Health Monitoring

#### Kubernetes Probes
```yaml
livenessProbe:
  httpGet:
    path: /api/health
    port: 8080
  initialDelaySeconds: 30
  periodSeconds: 10
  timeoutSeconds: 5
  successThreshold: 1
  failureThreshold: 3

readinessProbe:
  httpGet:
    path: /api/ready
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 5
  timeoutSeconds: 3
  successThreshold: 1
  failureThreshold: 3
```

#### External Monitoring
```bash
#!/bin/bash
# external-health-check.sh

ENDPOINT="http://go-mls.example.com/api/health"
SLACK_WEBHOOK="https://hooks.slack.com/services/YOUR/SLACK/WEBHOOK"

response=$(curl -s -o /dev/null -w "%{http_code}" "$ENDPOINT")

if [ "$response" != "200" ]; then
    curl -X POST "$SLACK_WEBHOOK" \
        -H 'Content-type: application/json' \
        --data "{\"text\":\"🚨 Go-MLS health check failed: HTTP $response\"}"
fi
```

---

## Alerting Configuration

### 1. Alertmanager Configuration

**alertmanager.yml:**
```yaml
global:
  smtp_smarthost: 'smtp.gmail.com:587'
  smtp_from: 'alerts@example.com'

route:
  group_by: ['alertname']
  group_wait: 10s
  group_interval: 10s
  repeat_interval: 1h
  receiver: 'default-receiver'
  routes:
  - match:
      severity: critical
    receiver: 'critical-receiver'

receivers:
- name: 'default-receiver'
  email_configs:
  - to: 'team@example.com'
    subject: '[Go-MLS] {{ .GroupLabels.alertname }}'
    body: |
      {{ range .Alerts }}
      Alert: {{ .Annotations.summary }}
      Description: {{ .Annotations.description }}
      {{ end }}

- name: 'critical-receiver'
  email_configs:
  - to: 'oncall@example.com'
    subject: '[CRITICAL] Go-MLS Alert'
  slack_configs:
  - api_url: 'https://hooks.slack.com/services/YOUR/SLACK/WEBHOOK'
    channel: '#alerts'
    title: 'Critical Go-MLS Alert'
    text: '{{ range .Alerts }}{{ .Annotations.summary }}{{ end }}'
```

### 2. Escalation Policies

```yaml
# Escalation based on alert duration
routes:
- match:
    severity: warning
  receiver: 'team-notification'
  continue: true
  
- match:
    severity: warning
  receiver: 'manager-notification'
  group_wait: 30m  # Escalate after 30 minutes
  
- match:
    severity: critical
  receiver: 'immediate-response'
  group_wait: 0s   # Immediate notification
```

---

## Troubleshooting Monitoring Issues

### 1. Common Prometheus Issues

```bash
# Check Prometheus targets
curl http://localhost:9090/api/v1/targets

# Verify metrics endpoint
curl http://localhost:8080/metrics

# Check Prometheus logs
docker logs prometheus
```

### 2. Common Grafana Issues

```bash
# Test data source connection
curl -H "Authorization: Bearer API_TOKEN" \
     http://localhost:3000/api/datasources/proxy/1/api/v1/query?query=up

# Check Grafana logs
docker logs grafana
```

### 3. Metric Collection Issues

```bash
# Verify metrics are being generated
curl -s http://localhost:8080/metrics | grep go_mls

# Check metric cardinality
curl -s http://localhost:9090/api/v1/label/__name__/values | jq '.data | length'
```

This monitoring guide provides comprehensive coverage of observability practices for Go-MLS, ensuring you can effectively monitor, alert on, and troubleshoot your streaming infrastructure.
