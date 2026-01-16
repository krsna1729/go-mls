# Observability Guide

This guide details the monitoring and observability infrastructure implemented in `go-mls`.

## 1. Architecture

The observability stack consists of:
- **Prometheus**: Collects metrics from the `go-mls` application and other services.
- **Grafana**: Visualizes metrics through interactive dashboards.
- **Go-MLS Exporter**: Built-in `/metrics` endpoint exposing Prometheus-compatible metrics.

### Data Flow
1. `go-mls` instruments code to update internal counters/gauges.
2. `go-mls` exposes these at `http://host:port/metrics`.
3. Prometheus scrapes this endpoint every 15s (configured in `provisioning/prometheus.yml`).
4. Grafana queries Prometheus to render dashboards.

---

## 2. Infrastructure Configuration

### Docker Compose
The `docker-compose.yml` includes:
- **Prometheus** (`prom/prometheus`): Port 9090. Configured with `extra_hosts` to access the host machine.
- **Grafana** (`grafana/grafana`): Port 3000. Pre-provisioned with data sources and dashboards.

### Prometheus Config
Location: `provisioning/prometheus.yml`
- Scrape Job: `go-mls`
- Target: `host.docker.internal:8080` (assumes app runs on host port 8080)

---

## 3. Metrics Reference

All metrics are prefixed with `gomls_`.

### Relay Metrics
| Metric Name | Type | Labels | Description |
|---|---|---|---|
| `gomls_active_input_relays` | Gauge | None | Current count of active input relays |
| `gomls_active_output_relays` | Gauge | None | Current count of active output relays |
| `gomls_relay_start_total` | Counter | `type` (input/output) | Total start attempts |
| `gomls_relay_stop_total` | Counter | `type`, `reason` | Total stop events |
| `gomls_relay_errors_total` | Counter | `type`, `error_type` | Total relay failures |

### Recording Metrics
| Metric Name | Type | Labels | Description |
|---|---|---|---|
| `gomls_active_recordings` | Gauge | None | Current count of active recordings |
| `gomls_recording_start_total` | Counter | None | Total recordings started |
| `gomls_recording_stop_total` | Counter | None | Total recordings stopped |
| `gomls_recording_bytes_total` | Counter | None | Total bytes recorded |

### HLS Metrics
| Metric Name | Type | Labels | Description |
|---|---|---|---|
| `gomls_active_hls_sessions` | Gauge | None | Current active HLS sessions |
| `gomls_active_hls_viewers` | Gauge | None | Current active HLS viewers |
| `gomls_hls_sessions_total` | Counter | None | Total HLS sessions created |

### System & Process Metrics
| Metric Name | Type | Labels | Description |
|---|---|---|---|
| `gomls_ffmpeg_processes_active` | Gauge | None | Total active FFmpeg processes |
| `gomls_ffmpeg_process_duration_seconds` | Histogram | `type` | Duration of completed processes |
| `gomls_ffmpeg_process_error_total` | Counter | `type`, `exit_code` | Total FFmpeg crashes/errors |
| `gomls_system_cpu_percent` | Gauge | None | System CPU usage (0-100) |
| `gomls_system_memory_bytes` | Gauge | None | System memory usage |

### HTTP Metrics (Middleware)
| Metric Name | Type | Labels | Description |
|---|---|---|---|
| `gomls_http_requests_total` | Counter | `method`, `path`, `status` | Total HTTP requests |
| `gomls_http_request_duration_seconds` | Histogram | `method`, `path` | Request latency distribution |
| `gomls_http_requests_in_flight` | Gauge | None | Content active requests |

*Note: Paths are normalized (e.g., IDs removed) to prevent high cardinality.*

---

## 4. Dashboards

A comprehensive Grafana dashboard is provisioned at `provisioning/dashboards/go-mls.json`.

### Panels
1.  **Active Input/Output Relays**: Real-time gauges.
2.  **Active Recordings**: Current recording count.
3.  **HLS Sessions/Viewers**: Live streaming stats.
4.  **Active FFmpeg Processes**: Health check for background workers.
5.  **HTTP Request Rate**: Requests per second by status code (200, 400, 500).
6.  **Error Rates**: Combined view of Relay logic errors and FFmpeg process crashes.

## 5. Usage

1. Start Infrastructure:
   ```bash
   docker-compose up -d
   ```
2. Start Application:
   ```bash
   go run main.go
   ```
3. View Metrics:
   - **Prometheus**: http://localhost:9090
   - **Grafana**: http://localhost:3000 (Login: `admin`/`admin`)
