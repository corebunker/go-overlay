# FastAPI + React + Caddy Stack

This example demonstrates a modern production stack with:
- **Migration**: Alembic for database migrations (runs once)
- **Backend**: FastAPI (Python) on port 8000
- **Frontend**: React with Vite built, served by Caddy on port 3000
- **Proxy**: Caddy does reverse proxy from `/api/*` to FastAPI

## Project Structure

```
project/
├── Dockerfile
├── services.toml
├── Caddyfile
├── backend/
│   ├── app/
│   │   ├── __init__.py
│   │   └── main.py
│   ├── alembic/
│   │   ├── versions/
│   │   └── env.py
│   ├── alembic.ini
│   └── requirements.txt
└── frontend/
    ├── package.json
    ├── vite.config.js
    └── dist/          # React build
        ├── index.html
        └── assets/
```

## services.toml

```toml
[timeouts]
service_shutdown_timeout = 10
global_shutdown_timeout = 30
dependency_wait_timeout = 60

# Database Migration (runs once and exits)
[[services]]
name = "db-migration"
command = "/usr/local/bin/alembic"
args = ["upgrade", "head"]
enabled = true
required = false
oneshot = true
depends_on = []

[[services]]
name = "fastapi-backend"
command = "/usr/local/bin/uvicorn"
args = ["app.main:app", "--host", "0.0.0.0", "--port", "8000"]
enabled = true
required = true
depends_on = ["db-migration"]
wait_after = 1

[[services]]
name = "caddy-frontend"
command = "/usr/bin/caddy"
args = ["run", "--config", "/etc/caddy/Caddyfile"]
enabled = true
required = true
```

## Caddyfile

```caddy
:3000 {
    root * /var/www
    encode gzip
    
    # API proxy
    reverse_proxy /api/* localhost:8000
    
    # React Router SPA fallback
    try_files {path} /index.html
    file_server
}
```

## Example: backend/app/main.py

```python
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

app = FastAPI()

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)

@app.get("/api/health")
def health():
    return {"status": "healthy"}

@app.get("/api/users")
def get_users():
    return [
        {"id": 1, "name": "Alice"},
        {"id": 2, "name": "Bob"}
    ]
```

## Dockerfile

```dockerfile
FROM ubuntu:22.04

# Install system dependencies
RUN apt-get update && \
    apt-get install -y python3 python3-pip curl debian-keyring debian-archive-keyring apt-transport-https && \
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg && \
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list && \
    apt-get update && apt-get install -y caddy && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Install Python dependencies
RUN pip3 install fastapi uvicorn[standard] alembic psycopg2-binary sqlalchemy

# Download go-overlay
ADD https://github.com/srelabz/go-overlay/releases/latest/download/go-overlay /go-overlay
RUN chmod +x /go-overlay

# Create directories
RUN mkdir -p /app /var/www /etc/caddy

# Copy backend
COPY backend/ /app/
WORKDIR /app

# Copy frontend build
COPY frontend/dist/ /var/www/

# Copy configs
COPY Caddyfile /etc/caddy/Caddyfile
COPY services.toml /services.toml

EXPOSE 3000 8000

ENTRYPOINT ["/go-overlay"]
```

## How to Run

1. **Build Frontend:**
   ```bash
   cd frontend
   npm install
   npm run build
   ```

2. **Build Docker image:**
   ```bash
   docker build -t fastapi-react-app .
   ```

3. **Run:**
   ```bash
   docker run -p 3000:3000 -p 8000:8000 fastapi-react-app
   ```

4. **Access:**
   - Frontend: http://localhost:3000
   - Backend API: http://localhost:8000/api/health
   - FastAPI Docs: http://localhost:8000/docs

## Startup Sequence

```
T+0s:  go-overlay starts
T+0s:  db-migration starts and executes migrations
T+2s:  db-migration completes (oneshot)
T+3s:  fastapi-backend starts (waits 1s after migration)
T+3s:  caddy-frontend starts
T+4s:  Complete stack running!
```

## CLI Commands

```bash
# List services
docker exec <container-id> go-overlay list

# View status
docker exec <container-id> go-overlay status

# Restart backend
docker exec <container-id> go-overlay restart fastapi-backend
```

## Features

- ✅ Automatic migrations on startup
- ✅ Hot reload in development (configure uvicorn with --reload)
- ✅ Automatic reverse proxy
- ✅ SPA routing with fallback
- ✅ CORS configured
- ✅ Gzip compression
