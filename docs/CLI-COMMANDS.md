# Go Overlay - CLI Commands Reference

This document provides a comprehensive guide to all CLI commands available in Go Overlay.

## Overview

Go Overlay operates in two modes:
1. **Daemon Mode**: Runs as the main process, managing services
2. **CLI Mode**: Sends commands to the running daemon via IPC

## Installation

### Automatic Installation (Recommended)
```bash
# When running in daemon mode, go-overlay auto-installs itself:
sudo go-overlay  # Creates symlink at /go-overlay
```

### Manual Installation
```bash
# Copy binary to PATH
sudo cp go-overlay /go-overlay
sudo chmod +x /go-overlay

# Or use the install command
go-overlay install
```

## Command Reference

### 1. Daemon Mode (Main Process)

Start go-overlay as the main service manager:

```bash
# Basic daemon mode
go-overlay

# With debug output
go-overlay --debug

# Docker usage
docker run -v $(pwd)/services.toml:/services.toml your-image go-overlay
```

**What happens in daemon mode:**
- Loads configuration from `/services.toml`
- Starts all enabled services
- Sets up graceful shutdown handlers
- Creates IPC socket for CLI communication
- Auto-installs symlink in PATH

### 2. List Services

Display current status of all services:

```bash
go-overlay list
```

**Example output:**
```
NAME            STATE      PID      UPTIME       REQUIRED LAST_ERROR
nginx           RUNNING    1234     5m23s        Yes      
php-fpm         RUNNING    1235     5m18s        No       
worker          FAILED     0        0s           No       connection refused
logger          STOPPING   1236     1m45s        No       
```

**Columns explained:**
- **NAME**: Service name from configuration
- **STATE**: Current service state (PENDING, STARTING, RUNNING, STOPPING, STOPPED, FAILED)
- **PID**: Process ID (0 if not running)
- **UPTIME**: How long the service has been running
- **REQUIRED**: Whether service failure stops the whole system
- **LAST_ERROR**: Most recent error message (if any)

### 3. System Status

Show overall system health:

```bash
go-overlay status
```

**Example output:**
```
System Status: Total: 4, Running: 2, Failed: 1
```

**Status summary:**
- **Total**: Number of active services
- **Running**: Services currently running
- **Failed**: Services in failed state

### 4. Restart Service

Restart a specific service:

```bash
go-overlay restart <service-name>

# Examples:
go-overlay restart nginx
go-overlay restart php-fpm
go-overlay restart worker
```

**Restart process:**
1. Sends SIGTERM to current process
2. Waits for graceful shutdown (configurable timeout)
3. Force kills if necessary
4. Starts new instance with original configuration
5. Returns success/failure message

**Example output:**
```bash
$ go-overlay restart nginx
Service 'nginx' restart initiated
```

### 5. Manual Installation

Install go-overlay in system PATH:

```bash
go-overlay install
```

**What this does:**
- Creates symlink at `/go-overlay`
- Enables global CLI usage
- Shows success/failure message

### 6. Release Upload to Backblaze B2 (via invoke)

Build and upload the release artifact to a Backblaze B2 S3-compatible bucket using the built-in tasks.

```bash
# Create the release package (artifact + files)
mise exec -- invoke release.release

# Export credentials (or use a .env file)
export BACKBLAZE_BUCKET="go-overlay"
export BACKBLAZE_ACCESS_KEY_ID="<your-key-id>"
export BACKBLAZE_SECRET_ACCESS_KEY="<your-app-key>"
export BACKBLAZE_ENDPOINT="s3.us-east-005.backblazeb2.com"  # default
export EXPIRATION=3600                                        # default

# Upload the artifact and print presigned/public URLs
mise exec -- invoke release.upload
```

Notes:
- The artifact name used is `go-overlay-linux-amd64` (created under `./release`).
- You may optionally set `OBJECT_NAME` to override the object name in the bucket.

### 7. Direct Download from Backblaze (Binary)

You can download the latest uploaded binary directly from Backblaze B2 using the public URL:

```text
https://f005.backblazeb2.com/file/go-overlay/go-overlay-linux-amd64
```

#### Using curl

```bash
curl -L "https://f005.backblazeb2.com/file/go-overlay/go-overlay-linux-amd64" -o go-overlay
chmod +x go-overlay
sudo mv go-overlay /usr/local/bin/

# Verify
go-overlay --help
```

#### Using wget

```bash
wget "https://f005.backblazeb2.com/file/go-overlay/go-overlay-linux-amd64" -O go-overlay
chmod +x go-overlay
sudo mv go-overlay /usr/local/bin/
```

#### Dockerfile example

```dockerfile
FROM alpine:latest

# Download from Backblaze B2
ADD https://f005.backblazeb2.com/file/go-overlay/go-overlay-linux-amd64 /go-overlay
RUN chmod +x /go-overlay

# Copy your configuration
COPY services.toml /services.toml

ENTRYPOINT ["/go-overlay"]
```

Notes:
- The file is built for Linux amd64. Ensure your target environment matches.
- If you rotate artifacts or bucket permissions, the URL may change or become unavailable. Prefer presigned URLs or versioned object names for production flows.

## Usage Patterns

### Basic Container Setup

```dockerfile
FROM alpine:latest

# Install go-overlay
ADD https://github.com/srelabz/go-overlay/releases/latest/download/go-overlay /go-overlay
RUN chmod +x /go-overlay

# Copy configuration
COPY services.toml /services.toml

# Set as entrypoint
ENTRYPOINT ["/go-overlay"]
```

### Multi-Terminal Management

**Terminal 1 (Daemon):**
```bash
go-overlay --debug
```

**Terminal 2 (Management):**
```bash
# Check status
go-overlay status

# List all services
go-overlay list

# Restart problematic service
go-overlay restart worker

# Check again
go-overlay list
```

### Automated Monitoring Script

```bash
#!/bin/bash
# monitor-services.sh

while true; do
  echo "=== $(date) ==="
  go-overlay status
  echo ""
  
  # Restart failed services
  go-overlay list | grep FAILED | awk '{print $1}' | while read service; do
    echo "Restarting failed service: $service"
    go-overlay restart "$service"
  done
  
  sleep 30
done
```

## Error Handling

### Common Issues

1. **"Could not connect to Go Overlay daemon"**
   - Daemon is not running
   - IPC socket file missing/corrupted
   - **Solution**: Start daemon first: `go-overlay`

2. **"Service 'xyz' not found"**
   - Service name doesn't exist in configuration
   - **Solution**: Check `services.toml` and use exact service name

3. **"Permission denied"**
   - Insufficient permissions for IPC socket
   - **Solution**: Run with appropriate user permissions

### Debug Mode

Enable debug output for troubleshooting:

```bash
go-overlay --debug
```

**Debug output includes:**
- Environment variables
- Service state transitions
- IPC communication details
- Shutdown sequence information
- Error details and stack traces

## Configuration Integration

CLI commands work with service configurations from `services.toml`:

```toml
[timeouts]
service_shutdown_timeout = 10  # Affects restart command timing
global_shutdown_timeout = 30   # Affects daemon shutdown

[[services]]
name = "web"                   # Used in: go-overlay restart web
command = "nginx"
required = true                # Shown in: go-overlay list
enabled = true                 # Controls if service starts

[[services]]
name = "worker"                # Used in: go-overlay restart worker
command = "/app/worker"
required = false               # Shown in: go-overlay list
enabled = false                # Service won't start, won't appear in list
oneshot = true                 # Optional for one-time jobs (e.g., migrations)
```

## Best Practices

### 1. Service Naming
- Use descriptive, unique names
- Avoid spaces and special characters
- Use kebab-case: `web-server`, `background-worker`

### 2. Monitoring Workflow
```bash
# Regular health check
go-overlay status

# Detailed investigation
go-overlay list

# Targeted restart
go-overlay restart failing-service

# Verify fix
go-overlay list | grep failing-service
```

### 3. Graceful Operations
- Always use `go-overlay restart` instead of killing processes directly
- Monitor logs during restarts
- Wait for services to stabilize before multiple operations

### 4. Integration with Monitoring
```bash
# Export metrics to monitoring system
go-overlay status | grep -o '[0-9]*' | paste -sd ',' -
# Output: 4,3,1 (total,running,failed)

# Alert on failures
failed_count=$(go-overlay list | grep -c FAILED)
if [ "$failed_count" -gt 0 ]; then
  echo "ALERT: $failed_count services failed"
fi
```
