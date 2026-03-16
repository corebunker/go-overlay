# Go Overlay - Quick Installation Guide

Multiple easy ways to get Go Overlay running in your container environment.

## Method 1: Direct GitHub Download (Recommended)

### Single Command Install
```bash
# Download and install
curl -L https://github.com/corebunker/go-overlay/releases/latest/download/go-overlay -o go-overlay
chmod +x go-overlay
sudo mv go-overlay /usr/local/bin/

# Now available globally
go-overlay --help
```

### Docker Integration
```dockerfile
FROM alpine:latest

# Download go-overlay directly from GitHub
ADD https://github.com/corebunker/go-overlay/releases/latest/download/go-overlay /go-overlay
RUN chmod +x /go-overlay

# Copy your service configuration
COPY services.toml /services.toml

# Set as entrypoint
ENTRYPOINT ["/go-overlay"]
```

## Method 2: Version-Specific Install

### For Specific Version
```bash
VERSION="v0.0.5"
curl -L "https://github.com/corebunker/go-overlay/releases/download/${VERSION}/go-overlay" -o go-overlay
chmod +x go-overlay
```

### Docker with Specific Version
```dockerfile
FROM alpine:latest

ARG VERSION=v0.0.5
ADD https://github.com/corebunker/go-overlay/releases/download/${VERSION}/go-overlay /go-overlay
RUN chmod +x /go-overlay

COPY services.toml /services.toml
ENTRYPOINT ["/go-overlay"]
```

Build with specific version:
```bash
docker build --build-arg VERSION=v0.0.5 -t myapp .
```

## Method 3: Build from Source

```bash
# Clone and build
git clone https://github.com/corebunker/go-overlay.git
cd go-overlay

# Install toolchain and deps via mise (Go/Python)
mise install

# Build and install using invoke
mise exec -- invoke go.build
mise exec -- invoke install

# Now available as go-overlay command
go-overlay --help
```

## Quick Test Setup

### 1. Create services.toml
```toml
[[services]]
name = "web"
command = "python3"
args = ["-m", "http.server", "8080"]
enabled = true

[[services]]
name = "logger"
command = "sh"
args = ["-c", "while true; do echo 'Log: '$(date); sleep 5; done"]
enabled = true
```

### 2. Run Go Overlay
```bash
# Option A: Direct run
go-overlay

# Option B: With debug output
go-overlay --debug

# Option C: Docker
docker run -d --name test-supervisor -p 8080:8080 \
  -v $(pwd)/services.toml:/services.toml \
  your-image
```

### 3. Test CLI Commands
```bash
# In another terminal (if running locally)
go-overlay list
go-overlay status
go-overlay restart web
```

## Auto-Installation Feature

When you run go-overlay in daemon mode, it automatically:

1. **Detects** if it's already in a PATH directory
2. **Creates** symlink at `/go-overlay`
3. **Enables** CLI commands from anywhere

```bash
# First run auto-installs
sudo ./go-overlay

# Now works from anywhere
go-overlay list
go-overlay restart nginx
```

## Real-World Example

### NGINX + PHP-FPM Stack

**services.toml:**
```toml
[timeouts]
service_shutdown_timeout = 10
global_shutdown_timeout = 30

[[services]]
name = "php-fpm"
command = "php-fpm8"
args = ["--nodaemonize"]
user = "www-data"
required = true

[[services]]
name = "nginx"
command = "nginx"
args = ["-g", "daemon off;"]
depends_on = "php-fpm"
wait_after = 3
required = true
```

**Dockerfile:**
```dockerfile
FROM php:8.1-fpm-alpine

# Install nginx
RUN apk add --no-cache nginx

# Install go-overlay
ADD https://github.com/corebunker/go-overlay/releases/latest/download/go-overlay /go-overlay
RUN chmod +x /go-overlay

# Copy configuration
COPY services.toml /services.toml
COPY nginx.conf /etc/nginx/nginx.conf

# Expose port
EXPOSE 80

# Start with go-overlay
ENTRYPOINT ["/go-overlay"]
```

**Build and run:**
```bash
docker build -t php-nginx .
docker run -p 80:80 php-nginx
```

**Manage services:**
```bash
docker exec -it <container> go-overlay list
docker exec -it <container> go-overlay restart nginx
```

## Troubleshooting

### Permission Issues
```bash
# Fix permissions
sudo chown root:root /go-overlay
sudo chmod 755 /go-overlay
```

### Path Issues
```bash
# Manually add to PATH
export PATH="/usr/local/bin:$PATH"

# Or create symlink
sudo ln -sf $(pwd)/go-overlay /go-overlay
```

### Service Won't Start
```bash
# Check configuration
go-overlay --debug

# Validate syntax
cat services.toml | grep -E "name|command"
```

## Integration Examples

### Docker Compose
```yaml
version: '3.8'
services:
  app:
    build: .
    ports:
      - "80:80"
    volumes:
      - ./services.toml:/services.toml
    entrypoint: ["/go-overlay"]
    
  # Management sidecar
  manager:
    build: .
    volumes:
      - ./services.toml:/services.toml
    entrypoint: ["sleep", "infinity"]
    depends_on:
      - app
```

Manage services:
```bash
docker-compose exec manager go-overlay list
docker-compose exec manager go-overlay restart nginx
```

### Kubernetes Init Container
```yaml
apiVersion: v1
kind: Pod
spec:
  initContainers:
  - name: install-go-overlay
    image: alpine/curl
    command:
    - sh
    - -c
    - |
      curl -L https://github.com/corebunker/go-overlay/releases/latest/download/go-overlay \
        -o /shared/go-overlay
      chmod +x /shared/go-overlay
    volumeMounts:
    - name: shared
      mountPath: /shared
      
  containers:
  - name: app
    image: your-app
    command: ["/shared/go-overlay"]
    volumeMounts:
    - name: shared
      mountPath: /shared
      
  volumes:
  - name: shared
    emptyDir: {}
```

## Next Steps

1. **Configure** your services in `services.toml`
2. **Test** with `go-overlay --debug`
3. **Deploy** using your preferred method
4. **Monitor** with CLI commands
5. **Scale** by adjusting service configuration

## Resources

- **[CLI Commands](CLI-COMMANDS.md)** - Complete command reference
- **[Configuration Guide](README.md)** - Service configuration options
- **[GitHub Releases](https://github.com/corebunker/go-overlay/releases)** - Download specific versions 

## Uploading Release Artifacts to Backblaze B2

You can upload the built artifact to a Backblaze B2 S3-compatible bucket.

### Using invoke tasks (recommended)

```bash
# Create the release artifact (generates ./release/go-overlay-linux-amd64)
mise exec -- invoke release.release

# Option A: export env vars
export BACKBLAZE_BUCKET="go-overlay"
export BACKBLAZE_ACCESS_KEY_ID="<your-key-id>"
export BACKBLAZE_SECRET_ACCESS_KEY="<your-app-key>"

# Optional overrides
export BACKBLAZE_ENDPOINT="s3.us-east-005.backblazeb2.com"
export EXPIRATION=3600
# export OBJECT_NAME="go-overlay-linux-amd64"   # optional override

# Option B: use a .env file (auto-loaded by the task)
cat > .env << 'EOF'
BACKBLAZE_BUCKET=go-overlay
BACKBLAZE_ACCESS_KEY_ID=<your-key-id>
BACKBLAZE_SECRET_ACCESS_KEY=<your-app-key>
BACKBLAZE_ENDPOINT=s3.us-east-005.backblazeb2.com
EXPIRATION=3600
# OBJECT_NAME=go-overlay-linux-amd64
EOF

# Upload the artifact and print URLs
mise exec -- invoke release.upload
```
