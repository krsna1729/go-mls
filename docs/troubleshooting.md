# Go-MLS Troubleshooting Guide

This guide provides solutions to common issues and debugging techniques for Go-MLS deployments.

---

## Common Issues and Solutions

### 1. Stream Connection Issues

#### Problem: Cannot connect to input stream
```
[ERROR] Failed to start input relay: rtmp://source.example.com/live/stream: connection timeout
```

**Possible Causes & Solutions:**

1. **Network connectivity**
   ```bash
   # Test network connectivity
   ping source.example.com
   telnet source.example.com 1935
   
   # Check firewall rules
   sudo ufw status
   sudo iptables -L
   ```

2. **RTMP URL format**
   ```bash
   # Correct format
   rtmp://server.com/app/streamkey
   
   # Common mistakes
   rtmp://server.com:1935app/streamkey  # Missing slash
   rtmp://server.com/app streamkey      # Space instead of slash
   ```

3. **Authentication issues**
   ```bash
   # Include credentials in URL if required
   rtmp://username:password@server.com/app/streamkey
   ```

4. **ffmpeg input options**
   ```json
   {
     "ffmpeg_options": {
       "rtmp_live": "1",
       "timeout": "10000000"
     }
   }
   ```

#### Problem: Stream disconnects frequently
```
[WARN] Input relay disconnected, attempting reconnect in 5s
```

**Solutions:**

1. **Increase buffer sizes**
   ```json
   {
     "ffmpeg_options": {
       "buffer_size": "8M",
       "max_delay": "5000000"
     }
   }
   ```

2. **Network optimization**
   ```bash
   # Increase network buffers
   echo 'net.core.rmem_max = 67108864' >> /etc/sysctl.conf
   echo 'net.core.wmem_max = 67108864' >> /etc/sysctl.conf
   sysctl -p
   ```

3. **Use more reliable connection options**
   ```json
   {
     "ffmpeg_options": {
       "rtmp_conn": "S:version,S:3,S:swfUrl,S:rtmp://server.com/app"
     }
   }
   ```

---

### 2. Output Relay Issues

#### Problem: Output stream fails to start
```
[ERROR] Failed to start output relay: rtmp://a.rtmp.youtube.com/live2/KEY: permission denied
```

**Possible Causes & Solutions:**

1. **Invalid stream key**
   ```bash
   # Verify stream key is correct
   # Check platform dashboard for correct key
   # Ensure no extra characters or spaces
   ```

2. **Platform restrictions**
   ```bash
   # YouTube: Verify channel is enabled for live streaming
   # Twitch: Check if account has streaming permissions
   # Facebook: Verify page has live streaming enabled
   ```

3. **Bitrate/quality restrictions**
   ```json
   {
     "platform_preset": "youtube",
     "ffmpeg_options": {
       "maxrate": "6000k",
       "bufsize": "12000k"
     }
   }
   ```

#### Problem: Stream quality issues
```
[WARN] Low bitrate detected: 0.5 Mbps
```

**Solutions:**

1. **Check input quality**
   ```bash
   # Analyze input stream
   ffprobe -v quiet -show_format -show_streams rtmp://input.url
   ```

2. **Adjust encoding settings**
   ```json
   {
     "ffmpeg_options": {
       "preset": "medium",
       "crf": "23",
       "bitrate": "5000k"
     }
   }
   ```

3. **Network bandwidth**
   ```bash
   # Test bandwidth
   iperf3 -c remote-server
   
   # Monitor network usage
   iftop -i eth0
   ```

---

### 3. Recording Issues

#### Problem: Recording fails to start
```
[ERROR] Recording failed: MainCamera: no such file or directory
```

**Solutions:**

1. **Check directory permissions**
   ```bash
   # Ensure recordings directory exists and is writable
   sudo mkdir -p /opt/go-mls/recordings
   sudo chown go-mls:go-mls /opt/go-mls/recordings
   sudo chmod 755 /opt/go-mls/recordings
   ```

2. **Verify disk space**
   ```bash
   # Check available space
   df -h /opt/go-mls/recordings
   
   # Clean up old recordings if needed
   find /opt/go-mls/recordings -name "*.mp4" -mtime +7 -delete
   ```

3. **Check input stream availability**
   ```bash
   # Ensure input stream is active before recording
   curl http://localhost:8080/api/status | jq '.inputs'
   ```

#### Problem: Recording file corruption
```
[ERROR] Recording file corrupted: MainCamera_20250618_143000.mp4
```

**Solutions:**

1. **Use safer recording options**
   ```json
   {
     "ffmpeg_options": {
       "c": "copy",
       "avoid_negative_ts": "make_zero",
       "fflags": "+genpts"
     }
   }
   ```

2. **Check disk health**
   ```bash
   # Check disk errors
   dmesg | grep -i error
   
   # Run disk check
   sudo fsck /dev/sda1
   ```

---

### 4. Performance Issues

#### Problem: High CPU usage
```
[WARN] High CPU usage detected: 95%
```

**Solutions:**

1. **Optimize encoding settings**
   ```json
   {
     "platform_preset": "youtube",
     "ffmpeg_options": {
       "preset": "veryfast",
       "threads": "4"
     }
   }
   ```

2. **Use hardware acceleration**
   ```json
   {
     "ffmpeg_options": {
       "hwaccel": "vaapi",
       "vaapi_device": "/dev/dri/renderD128",
       "c:v": "h264_vaapi"
     }
   }
   ```

3. **Reduce concurrent streams**
   ```bash
   # Monitor active processes
   ps aux | grep ffmpeg | wc -l
   
   # Stop unnecessary relays
   curl -X POST http://localhost:8080/api/stop-relay \
     -d '{"input_name": "Camera1", "output_name": "Output1"}'
   ```

#### Problem: High memory usage
```
[WARN] Memory usage: 8.5GB
```

**Solutions:**

1. **Adjust Go runtime settings**
   ```bash
   export GOGC=50  # More aggressive garbage collection
   export GOMAXPROCS=4  # Limit CPU cores
   ```

2. **Reduce buffer sizes**
   ```json
   {
     "ffmpeg_options": {
       "buffer_size": "2M",
       "max_muxing_queue_size": "1024"
     }
   }
   ```

3. **Monitor memory leaks**
   ```bash
   # Check for memory leaks
   valgrind --tool=memcheck --leak-check=full ./go-mls
   ```

---

### 5. Configuration Issues

#### Problem: Configuration import fails
```
[ERROR] Failed to import configuration: invalid JSON format
```

**Solutions:**

1. **Validate JSON format**
   ```bash
   # Validate JSON
   cat relay_config.json | jq '.'
   
   # Common JSON errors
   # - Missing commas
   # - Trailing commas
   # - Unescaped quotes
   ```

2. **Check required fields**
   ```json
   {
     "inputs": [
       {
         "name": "Required",
         "url": "Required"
       }
     ],
     "endpoints": [
       {
         "input_name": "Required",
         "output_name": "Required", 
         "input_url": "Required",
         "output_url": "Required"
       }
     ]
   }
   ```

#### Problem: Platform preset not working
```
[WARN] Using copy mode instead of youtube preset
```

**Solutions:**

1. **Verify preset name**
   ```json
   {
     "platform_preset": "youtube"  // Not "YouTube" or "YOUTUBE"
   }
   ```

2. **Check for conflicting options**
   ```json
   {
     // Don't mix preset with conflicting options
     "platform_preset": "youtube",
     // "ffmpeg_options": { ... }  // Remove this
   }
   ```

---

### 6. Web UI Issues

#### Problem: Web UI not loading
```
[ERROR] Failed to load static assets
```

**Solutions:**

1. **Check embedded assets**
   ```bash
   # Verify assets are embedded
   strings go-mls | grep "web/"
   ```

2. **Browser cache issues**
   ```bash
   # Clear browser cache
   # Try incognito/private mode
   # Check browser console for errors
   ```

3. **Proxy configuration**
   ```nginx
   # Ensure proxy passes all requests
   location / {
     proxy_pass http://go-mls:8080;
     proxy_set_header Host $host;
   }
   ```

#### Problem: API requests failing
```
[ERROR] 500 Internal Server Error
```

**Solutions:**

1. **Check API logs**
   ```bash
   # View detailed logs
   journalctl -u go-mls -f | grep "api"
   ```

2. **Verify request format**
   ```bash
   # Test API manually
   curl -X POST http://localhost:8080/api/start-relay \
     -H "Content-Type: application/json" \
     -d '{"input_url": "...", "output_url": "..."}'
   ```

---

## Debugging Techniques

### 1. Enable Debug Logging

```bash
# Start with debug logging
./go-mls -log-level debug

# Or set environment variable
export GO_MLS_LOG_LEVEL=debug
```

### 2. ffmpeg Debug Information

```json
{
  "ffmpeg_options": {
    "loglevel": "debug",
    "report": "1"
  }
}
```

### 3. Network Analysis

```bash
# Monitor network traffic
sudo tcpdump -i any -w capture.pcap host source.example.com

# Analyze with Wireshark
wireshark capture.pcap

# Monitor bandwidth usage
iftop -i eth0
nload eth0
```

### 4. Process Monitoring

```bash
# Monitor ffmpeg processes
watch -n 1 'ps aux | grep ffmpeg'

# Check process tree
pstree -p $(pgrep go-mls)

# Monitor file descriptors
lsof -p $(pgrep go-mls)
```

### 5. System Resource Monitoring

```bash
# CPU and memory usage
top -p $(pgrep go-mls)

# I/O statistics
iostat -x 1

# Network statistics
ss -tuln | grep :8080
netstat -i
```

---

## Advanced Troubleshooting

### 1. Memory Analysis

```bash
# Generate heap dump
curl http://localhost:8080/debug/pprof/heap > heap.pprof

# Analyze with go tool
go tool pprof heap.pprof
```

### 2. Performance Profiling

```bash
# CPU profile
curl http://localhost:8080/debug/pprof/profile?seconds=30 > cpu.pprof

# Goroutine analysis
curl http://localhost:8080/debug/pprof/goroutine > goroutine.pprof

# Analyze profiles
go tool pprof cpu.pprof
```

### 3. Custom Diagnostics

Add diagnostic endpoints to your application:

```go
func diagnosticHandler(w http.ResponseWriter, r *http.Request) {
    diagnostics := struct {
        Goroutines    int               `json:"goroutines"`
        Memory        runtime.MemStats `json:"memory"`
        OpenFiles     int               `json:"open_files"`
        ActiveStreams int               `json:"active_streams"`
    }{}
    
    diagnostics.Goroutines = runtime.NumGoroutine()
    runtime.ReadMemStats(&diagnostics.Memory)
    
    json.NewEncoder(w).Encode(diagnostics)
}
```

---

## Error Code Reference

### HTTP API Error Codes

| Code | Description | Solution |
|------|-------------|----------|
| 400  | Bad Request | Check request format and required fields |
| 409  | Conflict | Resource already exists |
| 404  | Not Found | Resource doesn't exist |
| 500  | Internal Server Error | Check server logs |
| 503  | Service Unavailable | Server overloaded or starting |

### Application Error Codes

| Code | Description | Solution |
|------|-------------|----------|
| STREAM_NOT_FOUND | Stream doesn't exist | Verify stream name |
| PROCESS_FAILED | ffmpeg process failed | Check ffmpeg logs |
| NETWORK_ERROR | Network connection issue | Check connectivity |
| CONFIG_ERROR | Configuration error | Validate configuration |
| RESOURCE_ERROR | Resource limit exceeded | Check system resources |

---

## Performance Tuning Checklist

### System Level
- [ ] Sufficient CPU cores (2+ recommended)
- [ ] Adequate RAM (4GB+ recommended)
- [ ] Fast storage (SSD preferred)
- [ ] Network bandwidth (100Mbps+ for multiple streams)
- [ ] File descriptor limits increased
- [ ] Network buffer sizes optimized

### Application Level
- [ ] Debug logging disabled in production
- [ ] Appropriate Go runtime settings (GOGC, GOMAXPROCS)
- [ ] ffmpeg presets optimized for use case
- [ ] Resource monitoring enabled
- [ ] Health checks configured

### Stream Settings
- [ ] Bitrate appropriate for bandwidth
- [ ] Resolution suitable for target platform
- [ ] Encoding preset balanced for quality/performance
- [ ] Buffer sizes appropriate for network conditions

---

## Getting Help

### 1. Collect Diagnostic Information

Before seeking help, collect:

```bash
#!/bin/bash
# collect-diagnostics.sh

echo "=== System Information ===" > diagnostics.txt
uname -a >> diagnostics.txt
cat /etc/os-release >> diagnostics.txt

echo -e "\n=== Go-MLS Version ===" >> diagnostics.txt
./go-mls -version >> diagnostics.txt

echo -e "\n=== System Resources ===" >> diagnostics.txt
free -h >> diagnostics.txt
df -h >> diagnostics.txt
ps aux | grep go-mls >> diagnostics.txt

echo -e "\n=== Network Configuration ===" >> diagnostics.txt
ip addr show >> diagnostics.txt
ss -tuln >> diagnostics.txt

echo -e "\n=== Recent Logs ===" >> diagnostics.txt
journalctl -u go-mls --since "1 hour ago" >> diagnostics.txt

echo -e "\n=== Configuration ===" >> diagnostics.txt
cat relay_config.json >> diagnostics.txt

tar czf go-mls-diagnostics-$(date +%Y%m%d_%H%M%S).tar.gz diagnostics.txt
```

### 2. Support Channels

- **GitHub Issues**: Report bugs and feature requests
- **Documentation**: Check the docs/ directory
- **Community Forums**: Ask questions and share solutions
- **Professional Support**: Contact for enterprise support

### 3. Providing Effective Bug Reports

Include:
- Go-MLS version
- Operating system and version
- Steps to reproduce the issue
- Expected vs actual behavior
- Relevant log messages
- Configuration (sanitized)
- Network topology if relevant

This troubleshooting guide should help you diagnose and resolve most common issues with Go-MLS deployments.
