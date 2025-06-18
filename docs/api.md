# Go-MLS API Reference

## Base URL
All API endpoints are relative to: `http://localhost:8080/api`

## Content Types
- Request: `application/json`
- Response: `application/json`

## Authentication
Currently, no authentication is required. This can be extended for production use.

---

## Relay Management

### Start Relay
Start a new output relay for streaming to a destination.

**Endpoint:** `POST /start-relay`

**Request Body:**
```json
{
  "input_url": "rtmp://source.example.com/live/stream",
  "output_url": "rtmp://a.rtmp.youtube.com/live2/YOUR_KEY",
  "input_name": "MainCamera",
  "output_name": "YouTube",
  "platform_preset": "youtube",
  "ffmpeg_options": {
    "preset": "medium",
    "bitrate": "5000k"
  }
}
```

**Parameters:**
- `input_url` (string, required): Source stream URL
- `output_url` (string, required): Destination stream URL  
- `input_name` (string, required): Logical name for input stream
- `output_name` (string, required): Logical name for output stream
- `platform_preset` (string, optional): Platform optimization preset
- `ffmpeg_options` (object, optional): Custom ffmpeg parameters

**Platform Presets:**
- `youtube`: Optimized for YouTube streaming
- `instagram`: Vertical orientation for Instagram
- `tiktok`: Vertical orientation for TikTok
- `facebook`: Optimized for Facebook Live
- `twitch`: Optimized for Twitch streaming

**Response:**
```json
{
  "message": "Relay started successfully",
  "input_name": "MainCamera",
  "output_name": "YouTube"
}
```

**Error Responses:**
- `400 Bad Request`: Invalid request parameters
- `409 Conflict`: Relay already exists
- `500 Internal Server Error`: Server error

---

### Stop Relay
Stop an existing output relay.

**Endpoint:** `POST /stop-relay`

**Request Body:**
```json
{
  "input_name": "MainCamera",
  "output_name": "YouTube"
}
```

**Parameters:**
- `input_name` (string, required): Input stream name
- `output_name` (string, required): Output stream name

**Response:**
```json
{
  "message": "Relay stopped successfully"
}
```

---

### Get Relay Status
Get status of all active relays.

**Endpoint:** `GET /status`

**Response:**
```json
{
  "inputs": [
    {
      "name": "MainCamera",
      "url": "rtmp://source.example.com/live/stream",
      "status": "Running",
      "consumers": 2
    }
  ],
  "endpoints": [
    {
      "input_name": "MainCamera",
      "output_name": "YouTube",
      "input_url": "rtmp://source.example.com/live/stream",
      "output_url": "rtmp://a.rtmp.youtube.com/live2/KEY",
      "platform_preset": "youtube",
      "status": "Running",
      "last_bitrate": "5.8 Mbps"
    }
  ],
  "recordings": [
    {
      "name": "MainCamera",
      "source": "rtmp://source.example.com/live/stream",
      "status": "Recording",
      "start_time": "2025-06-18T14:30:00Z",
      "filename": "MainCamera_20250618_143000.mp4"
    }
  ]
}
```

---

## Recording Management

### Start Recording
Begin recording an input stream to disk.

**Endpoint:** `POST /start-recording`

**Request Body:**
```json
{
  "name": "MainCamera",
  "source": "rtmp://source.example.com/live/stream"
}
```

**Parameters:**
- `name` (string, required): Name for the recording
- `source` (string, required): Source stream URL

**Response:**
```json
{
  "message": "Recording started successfully",
  "name": "MainCamera",
  "filename": "MainCamera_20250618_143000.mp4"
}
```

---

### Stop Recording
Stop an active recording.

**Endpoint:** `POST /stop-recording`

**Request Body:**
```json
{
  "name": "MainCamera"
}
```

**Parameters:**
- `name` (string, required): Name of the recording to stop

**Response:**
```json
{
  "message": "Recording stopped successfully"
}
```

---

### List Recordings
Get a list of all available recordings.

**Endpoint:** `GET /recordings`

**Response:**
```json
{
  "recordings": [
    {
      "filename": "MainCamera_20250618_143000.mp4",
      "size": 1048576000,
      "created_at": "2025-06-18T14:30:00Z",
      "duration": 3600
    }
  ]
}
```

---

### Download Recording
Download a recorded file.

**Endpoint:** `GET /recordings/{filename}`

**Parameters:**
- `filename` (string, required): Name of the file to download

**Response:**
- Binary file download with appropriate headers
- `Content-Type: video/mp4`
- `Content-Disposition: attachment; filename="{filename}"`

---

## Configuration Management

### Export Configuration
Export current configuration to JSON.

**Endpoint:** `GET /export-config`

**Response:**
```json
{
  "inputs": [
    {
      "name": "MainCamera",
      "url": "rtmp://source.example.com/live/stream"
    }
  ],
  "endpoints": [
    {
      "input_name": "MainCamera",
      "output_name": "YouTube",
      "input_url": "rtmp://source.example.com/live/stream",
      "output_url": "rtmp://a.rtmp.youtube.com/live2/KEY",
      "platform_preset": "youtube",
      "ffmpeg_options": {}
    }
  ]
}
```

---

### Import Configuration
Import configuration from JSON and start all defined relays.

**Endpoint:** `POST /import-config`

**Request Body:**
```json
{
  "inputs": [
    {
      "name": "MainCamera",
      "url": "rtmp://source.example.com/live/stream"
    }
  ],
  "endpoints": [
    {
      "input_name": "MainCamera",
      "output_name": "YouTube",
      "input_url": "rtmp://source.example.com/live/stream",
      "output_url": "rtmp://a.rtmp.youtube.com/live2/KEY",
      "platform_preset": "youtube"
    }
  ]
}
```

**Response:**
```json
{
  "message": "Configuration imported successfully",
  "started_relays": 1
}
```

---

## Monitoring & Statistics

### Real-time Logs
Get streaming logs from the server.

**Endpoint:** `GET /logs` (Server-Sent Events)

**Response:**
```
data: {"timestamp":"2025-06-18T14:30:00Z","level":"INFO","message":"Relay started successfully"}

data: {"timestamp":"2025-06-18T14:30:01Z","level":"DEBUG","message":"ffmpeg bitrate: 5.8 Mbps"}
```

---

### System Health
Get system health and performance metrics.

**Endpoint:** `GET /health`

**Response:**
```json
{
  "status": "healthy",
  "uptime": 86400,
  "active_streams": 5,
  "cpu_usage": 45.2,
  "memory_usage": 67.8,
  "disk_usage": 23.4,
  "ffmpeg_processes": 6
}
```

---

## Error Handling

All API endpoints return appropriate HTTP status codes and error messages.

### Common Status Codes
- `200 OK`: Request successful
- `400 Bad Request`: Invalid request parameters
- `404 Not Found`: Resource not found
- `409 Conflict`: Resource already exists
- `500 Internal Server Error`: Server error

### Error Response Format
```json
{
  "error": "Error description",
  "code": "ERROR_CODE",
  "details": "Additional error details"
}
```

### Common Error Codes
- `INVALID_INPUT`: Invalid input parameters
- `RELAY_EXISTS`: Relay already running
- `RELAY_NOT_FOUND`: Relay not found
- `RECORDING_EXISTS`: Recording already active
- `RECORDING_NOT_FOUND`: Recording not found
- `FFMPEG_ERROR`: ffmpeg process error
- `STREAM_ERROR`: Stream connection error

---

## Rate Limiting

Currently, no rate limiting is implemented. For production use, consider implementing:
- Request rate limiting per IP
- Concurrent stream limits per user
- Resource usage monitoring

---

## Webhooks (Future)

Future versions may support webhooks for:
- Stream status changes
- Recording completion
- Error notifications
- Performance alerts

---

## SDK Examples

### cURL Examples

**Start a YouTube relay:**
```bash
curl -X POST http://localhost:8080/api/start-relay \
  -H "Content-Type: application/json" \
  -d '{
    "input_url": "rtmp://source.example.com/live/stream",
    "output_url": "rtmp://a.rtmp.youtube.com/live2/YOUR_KEY",
    "input_name": "MainCamera",
    "output_name": "YouTube",
    "platform_preset": "youtube"
  }'
```

**Start recording:**
```bash
curl -X POST http://localhost:8080/api/start-recording \
  -H "Content-Type: application/json" \
  -d '{
    "name": "MainCamera",
    "source": "rtmp://source.example.com/live/stream"
  }'
```

**Get status:**
```bash
curl http://localhost:8080/api/status
```

### JavaScript Examples

**Start relay with custom options:**
```javascript
const response = await fetch('/api/start-relay', {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json'
  },
  body: JSON.stringify({
    input_url: 'rtmp://source.example.com/live/stream',
    output_url: 'rtmp://a.rtmp.youtube.com/live2/YOUR_KEY',
    input_name: 'MainCamera',
    output_name: 'YouTube',
    ffmpeg_options: {
      'preset': 'medium',
      'bitrate': '8000k',
      'fps': '60'
    }
  })
});

const result = await response.json();
console.log(result);
```

### Python Examples

**Using requests library:**
```python
import requests

# Start relay
response = requests.post('http://localhost:8080/api/start-relay', json={
    'input_url': 'rtmp://source.example.com/live/stream',
    'output_url': 'rtmp://a.rtmp.youtube.com/live2/YOUR_KEY',
    'input_name': 'MainCamera',
    'output_name': 'YouTube',
    'platform_preset': 'youtube'
})

print(response.json())

# Get status
status = requests.get('http://localhost:8080/api/status')
print(status.json())
```
