# Express.js + React + Caddy Stack

This example demonstrates a full-stack Node.js with:
- **Migration**: Prisma migrations for database (runs once)
- **Backend**: Express.js on port 4000
- **Frontend**: React built, served by Caddy on port 3000
- **Proxy**: Caddy does reverse proxy from `/api/*` to Express

## Project Structure

```
project/
├── Dockerfile
├── services.toml
├── Caddyfile
├── backend/
│   ├── package.json
│   ├── package-lock.json
│   ├── server.js
│   ├── prisma/
│   │   └── schema.prisma
│   └── node_modules/
└── frontend/
    ├── package.json
    ├── vite.config.js
    └── dist/          # React build
```

## services.toml

```toml
[timeouts]
service_shutdown_timeout = 10
global_shutdown_timeout = 30
dependency_wait_timeout = 60

# Database Migration with Prisma (runs once and exits)
[[services]]
name = "db-migration"
command = "/usr/bin/npx"
args = ["prisma", "migrate", "deploy"]
enabled = true
required = false

[[services]]
name = "express-backend"
command = "/usr/bin/node"
args = ["/app/server.js"]
enabled = true
required = true
depends_on = "db-migration"
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
    
    reverse_proxy /api/* localhost:4000
    
    try_files {path} /index.html
    file_server
}
```

## Example: backend/server.js

```javascript
const express = require('express');
const cors = require('cors');

const app = express();

app.use(cors());
app.use(express.json());

app.get('/api/health', (req, res) => {
  res.json({ status: 'healthy' });
});

app.get('/api/users', (req, res) => {
  res.json([
    { id: 1, name: 'Alice' },
    { id: 2, name: 'Bob' }
  ]);
});

app.listen(4000, '0.0.0.0', () => {
  console.log('Express running on port 4000');
});
```

## Example: backend/prisma/schema.prisma

```prisma
datasource db {
  provider = "postgresql"
  url      = env("DATABASE_URL")
}

generator client {
  provider = "prisma-client-js"
}

model User {
  id        Int      @id @default(autoincrement())
  name      String
  email     String   @unique
  createdAt DateTime @default(now())
}
```

## Dockerfile

```dockerfile
FROM ubuntu:22.04

# Install Node.js and Caddy
RUN apt-get update && \
    apt-get install -y curl gnupg debian-keyring debian-archive-keyring apt-transport-https && \
    curl -fsSL https://deb.nodesource.com/setup_20.x | bash - && \
    apt-get install -y nodejs && \
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg && \
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list && \
    apt-get update && apt-get install -y caddy && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Download go-overlay
ADD https://github.com/corebunker/go-overlay/releases/latest/download/go-overlay /go-overlay
RUN chmod +x /go-overlay

# Create directories
RUN mkdir -p /app /var/www /etc/caddy

# Copy and install backend
COPY backend/package*.json /app/
WORKDIR /app
RUN npm ci --only=production && npx prisma generate
COPY backend/ /app/

# Copy frontend build
COPY frontend/dist/ /var/www/

# Copy configs
COPY Caddyfile /etc/caddy/Caddyfile
COPY services.toml /services.toml

EXPOSE 3000 4000

ENTRYPOINT ["/go-overlay"]
```

## How to Run

1. **Build Frontend:**
   ```bash
   cd frontend
   npm install
   npm run build
   ```

2. **Prepare Backend:**
   ```bash
   cd backend
   npm install
   npx prisma generate
   ```

3. **Build Docker image:**
   ```bash
   docker build -t express-react-app .
   ```

4. **Run with environment variables:**
   ```bash
   docker run -p 3000:3000 -p 4000:4000 \
     -e DATABASE_URL="postgresql://user:pass@host:5432/mydb" \
     express-react-app
   ```

5. **Access:**
   - Frontend: http://localhost:3000
   - Backend API: http://localhost:4000/api/health

## Startup Sequence

```
T+0s:  go-overlay starts
T+0s:  db-migration starts (prisma migrate deploy)
T+2s:  db-migration completes
T+3s:  express-backend starts (waits 1s after migration)
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
docker exec <container-id> go-overlay restart express-backend
```

## Features

- ✅ Prisma ORM with type-safety
- ✅ Automatic migrations on startup
- ✅ JavaScript/TypeScript full-stack
- ✅ Automatic reverse proxy
- ✅ SPA routing with fallback
- ✅ CORS configured
- ✅ Gzip compression
