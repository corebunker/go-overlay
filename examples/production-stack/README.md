# Complete Production Stack

This example demonstrates a complete production stack with all typical components:
- **Cache**: Redis on port 6379
- **Database**: PostgreSQL on port 5432
- **Migration**: Alembic migrations (runs once and exits)
- **Backend**: FastAPI with Uvicorn on port 8000
- **Frontend**: React with Vite served by Caddy on port 3000
- **Proxy**: Caddy with WebSocket support

## Project Structure

```
project/
├── Dockerfile
├── services.toml
├── Caddyfile
├── backend/
│   ├── app/
│   │   ├── __init__.py
│   │   ├── main.py
│   │   ├── database.py
│   │   └── cache.py
│   ├── alembic/
│   │   ├── versions/
│   │   │   └── 001_initial.py
│   │   └── env.py
│   ├── alembic.ini
│   └── requirements.txt
└── frontend/
    └── dist/
```

## services.toml

```toml
[timeouts]
service_shutdown_timeout = 15
global_shutdown_timeout = 45
dependency_wait_timeout = 90

# Redis Cache Service
[[services]]
name = "redis"
command = "/usr/bin/redis-server"
args = ["--bind", "0.0.0.0", "--port", "6379", "--maxmemory", "256mb", "--maxmemory-policy", "allkeys-lru"]
enabled = true
required = true

# PostgreSQL Database
[[services]]
name = "postgres"
command = "/usr/lib/postgresql/14/bin/postgres"
args = ["-D", "/var/lib/postgresql/data"]
enabled = true
required = true
user = "postgres"

# Database Migration (runs once and exits)
[[services]]
name = "db-migration"
command = "/usr/local/bin/alembic"
args = ["upgrade", "head"]
enabled = true
required = false
oneshot = true
depends_on = "postgres"
wait_after = 3
pre_script = """
#!/bin/bash
echo "Waiting for PostgreSQL to be ready..."
until pg_isready -h localhost -p 5432; do
  echo "PostgreSQL is unavailable - sleeping"
  sleep 1
done
echo "PostgreSQL is ready - running migrations"
"""

# FastAPI Backend
[[services]]
name = "fastapi-backend"
command = "/usr/local/bin/uvicorn"
args = ["app.main:app", "--host", "0.0.0.0", "--port", "8000"]
enabled = true
required = true
depends_on = "redis"
wait_after = 2
pre_script = """
#!/bin/bash
echo "Checking Redis connection..."
redis-cli -h localhost -p 6379 ping || exit 1
echo "Checking PostgreSQL connection..."
pg_isready -h localhost -p 5432 || exit 1
echo "All dependencies ready!"
"""

# Caddy Frontend
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
    
    # WebSocket support (if needed)
    @websockets {
        header Connection *Upgrade*
        header Upgrade websocket
    }
    reverse_proxy @websockets localhost:8000
    
    # React Router SPA fallback
    try_files {path} /index.html
    file_server
}
```

## Example: backend/app/main.py

```python
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
import redis
from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker
import os

app = FastAPI(title="Production Stack API")

# Redis connection
redis_client = redis.Redis(
    host='localhost', 
    port=6379, 
    decode_responses=True
)

# PostgreSQL connection
DATABASE_URL = os.getenv(
    "DATABASE_URL", 
    "postgresql://user:pass@localhost:5432/myapp"
)
engine = create_engine(DATABASE_URL)
SessionLocal = sessionmaker(bind=engine)

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)

@app.get("/api/health")
def health():
    """Health check endpoint - verifies all dependencies"""
    try:
        # Check Redis
        redis_client.ping()
        redis_status = "ok"
    except Exception as e:
        redis_status = f"error: {str(e)}"
    
    try:
        # Check PostgreSQL
        db = SessionLocal()
        db.execute("SELECT 1")
        db.close()
        postgres_status = "ok"
    except Exception as e:
        postgres_status = f"error: {str(e)}"
    
    return {
        "status": "healthy",
        "redis": redis_status,
        "postgres": postgres_status
    }

@app.get("/api/users")
def get_users():
    """Get users with Redis cache"""
    # Try cache first
    cached = redis_client.get("users")
    if cached:
        return {
            "source": "cache",
            "data": eval(cached)  # In production use json.loads
        }
    
    # Get from database
    db = SessionLocal()
    # ... query database
    users = [
        {"id": 1, "name": "Alice", "email": "alice@example.com"},
        {"id": 2, "name": "Bob", "email": "bob@example.com"}
    ]
    
    # Cache result for 5 minutes
    redis_client.setex("users", 300, str(users))
    
    db.close()
    return {
        "source": "database",
        "data": users
    }

@app.post("/api/users/clear-cache")
def clear_cache():
    """Clear user cache"""
    redis_client.delete("users")
    return {"message": "Cache cleared"}
```

## Example: alembic/versions/001_initial.py

```python
"""Initial migration

Revision ID: 001
Create Date: 2025-01-01 00:00:00
"""
from alembic import op
import sqlalchemy as sa

def upgrade():
    op.create_table(
        'users',
        sa.Column('id', sa.Integer, primary_key=True),
        sa.Column('name', sa.String(100), nullable=False),
        sa.Column('email', sa.String(255), unique=True, nullable=False),
        sa.Column('created_at', sa.DateTime, server_default=sa.func.now()),
    )

def downgrade():
    op.drop_table('users')
```

## Dockerfile

```dockerfile
FROM ubuntu:22.04

# Install system dependencies
RUN apt-get update && \
    apt-get install -y \
    python3 python3-pip \
    postgresql-14 postgresql-client-14 \
    redis-server redis-tools \
    curl gnupg debian-keyring debian-archive-keyring apt-transport-https && \
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg && \
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list && \
    apt-get update && apt-get install -y caddy && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Install Python dependencies
RUN pip3 install fastapi uvicorn[standard] alembic psycopg2-binary sqlalchemy redis

# Download go-overlay
ADD https://github.com/corebunker/go-overlay/releases/latest/download/go-overlay /go-overlay
RUN chmod +x /go-overlay

# Setup PostgreSQL
RUN mkdir -p /var/lib/postgresql/data && \
    chown -R postgres:postgres /var/lib/postgresql && \
    su - postgres -c "/usr/lib/postgresql/14/bin/initdb -D /var/lib/postgresql/data"

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

2. **Build image:**
   ```bash
   docker build -t production-stack .
   ```

3. **Run with volumes for persistence:**
   ```bash
   docker run -p 3000:3000 -p 8000:8000 \
     -v postgres-data:/var/lib/postgresql/data \
     -e DATABASE_URL="postgresql://user:pass@localhost:5432/myapp" \
     production-stack
   ```

4. **Access:**
   - Frontend: http://localhost:3000
   - Backend API: http://localhost:8000/api/health
   - FastAPI Docs: http://localhost:8000/docs

## Startup Sequence

```
T+0s:  go-overlay starts
T+0s:  Redis and PostgreSQL start in parallel
T+1s:  Redis is ready
T+2s:  PostgreSQL is ready
T+2s:  db-migration starts (waited 3s for postgres, but ran pre_script)
T+5s:  pre_script validates PostgreSQL with pg_isready
T+6s:  Migrations execute
T+7s:  db-migration completes (oneshot)
T+9s:  FastAPI starts (waited 2s for redis)
T+9s:  pre_script validates Redis and PostgreSQL
T+10s: FastAPI is ready
T+10s: Caddy starts
T+11s: Complete stack running! 🚀
```

## CLI Commands

```bash
# List all services
docker exec <container-id> go-overlay list

# View detailed status
docker exec <container-id> go-overlay status

# Restart only backend
docker exec <container-id> go-overlay restart fastapi-backend

# Real-time logs
docker logs -f <container-id>
```

## Monitoring

### Health Check

```bash
# Via curl
curl http://localhost:8000/api/health

# Via Docker healthcheck (add to Dockerfile)
HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
  CMD curl -f http://localhost:8000/api/health || exit 1
```

### Check individual services

```bash
# Redis
docker exec <container-id> redis-cli ping

# PostgreSQL
docker exec <container-id> pg_isready -h localhost -p 5432

# FastAPI
curl http://localhost:8000/api/health
```

## Data Persistence

### With Docker Volumes

```bash
# Create volume
docker volume create postgres-data

# Run with volume
docker run -v postgres-data:/var/lib/postgresql/data production-stack
```

### With Bind Mount

```bash
docker run -v /host/data:/var/lib/postgresql/data production-stack
```

## Environment Variables

```bash
docker run -p 3000:3000 -p 8000:8000 \
  -e DATABASE_URL="postgresql://user:pass@localhost:5432/myapp" \
  -e REDIS_URL="redis://localhost:6379" \
  -e SECRET_KEY="your-secret-key" \
  -e DEBUG="false" \
  production-stack
```

## Features

- ✅ Cache with Redis (LRU policy)
- ✅ PostgreSQL database
- ✅ Automatic migrations
- ✅ Health checks on all services
- ✅ Dependency validation with pre_script
- ✅ Graceful shutdown
- ✅ WebSocket support
- ✅ CORS configured
- ✅ Gzip compression
- ✅ Centralized logs

## When to use this stack?

- ✅ Applications that need cache
- ✅ Complex relational data
- ✅ High read performance
- ✅ User sessions
- ✅ Rate limiting
- ✅ Background jobs with queue
- ✅ Real-time features

## Next Steps

1. **Add authentication**: JWT with Redis for tokens
2. **Background workers**: Celery for async tasks
3. **Monitoring**: Add Prometheus + Grafana
4. **Logging**: Centralize logs with ELK stack
5. **Backups**: Automatic PostgreSQL backup script

## Additional Resources

- [FastAPI Documentation](https://fastapi.tiangolo.com/)
- [Redis Documentation](https://redis.io/documentation)
- [PostgreSQL Documentation](https://www.postgresql.org/docs/)
- [Caddy Documentation](https://caddyserver.com/docs/)

---

**Production-ready stack with all the essentials! 🚀**
