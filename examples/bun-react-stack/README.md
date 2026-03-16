# Bun + React + Caddy Stack (Performance Stack)

This example demonstrates a high-performance stack using Bun:
- **Backend**: API with Bun (ultra-fast JavaScript runtime) on port 4000
- **Frontend**: React built, served by Caddy on port 3000
- **Proxy**: Caddy does reverse proxy from `/api/*` to Bun
- **Performance**: ~3x faster startup than Node.js, lower memory usage

## What is Bun?

[Bun](https://bun.sh) is a modern JavaScript/TypeScript runtime that:
- ⚡ Is significantly faster than Node.js
- 📦 Has integrated bundler, transpiler and package manager
- 🔋 Uses less memory
- ✅ Is compatible with Node.js APIs
- 🚀 Instant startup

## Project Structure

```
project/
├── Dockerfile
├── services.toml
├── Caddyfile
├── backend/
│   ├── package.json
│   ├── server.ts      # Native TypeScript!
│   └── bun.lockb
└── frontend/
    ├── package.json
    └── dist/
```

## services.toml

```toml
[timeouts]
service_shutdown_timeout = 10
global_shutdown_timeout = 30

[[services]]
name = "bun-backend"
command = "/root/.bun/bin/bun"
args = ["run", "/app/server.ts"]
enabled = true
required = true

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

## Example: backend/server.ts

Native TypeScript without compilation needed!

```typescript
const server = Bun.serve({
  port: 4000,
  hostname: '0.0.0.0',
  
  fetch(req) {
    const url = new URL(req.url);
    
    // Health check
    if (url.pathname === "/api/health") {
      return Response.json({ 
        status: "healthy",
        runtime: "bun",
        version: Bun.version
      });
    }
    
    // Users endpoint
    if (url.pathname === "/api/users") {
      return Response.json([
        { id: 1, name: "Alice", email: "alice@example.com" },
        { id: 2, name: "Bob", email: "bob@example.com" }
      ]);
    }
    
    // POST example
    if (url.pathname === "/api/users" && req.method === "POST") {
      const body = await req.json();
      return Response.json({ 
        success: true, 
        user: body 
      }, { status: 201 });
    }
    
    return new Response("Not Found", { status: 404 });
  },
  
  // Error handling
  error(error) {
    return new Response(`Error: ${error.message}`, { status: 500 });
  },
});

console.log(`🚀 Bun server running on port ${server.port}`);
console.log(`📊 Memory usage: ${(process.memoryUsage().heapUsed / 1024 / 1024).toFixed(2)} MB`);
```

## Example with Database (SQLite with Bun)

Bun has native SQLite support!

```typescript
import { Database } from "bun:sqlite";

const db = new Database("mydb.sqlite");

// Create table
db.run(`
  CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    email TEXT UNIQUE NOT NULL
  )
`);

const server = Bun.serve({
  port: 4000,
  fetch(req) {
    const url = new URL(req.url);
    
    if (url.pathname === "/api/users") {
      const users = db.query("SELECT * FROM users").all();
      return Response.json(users);
    }
    
    if (url.pathname === "/api/users" && req.method === "POST") {
      const body = await req.json();
      const result = db.query(
        "INSERT INTO users (name, email) VALUES (?, ?)"
      ).run(body.name, body.email);
      
      return Response.json({ id: result.lastInsertRowid });
    }
    
    return new Response("Not Found", { status: 404 });
  },
});
```

## Dockerfile

```dockerfile
FROM ubuntu:22.04

# Install dependencies
RUN apt-get update && \
    apt-get install -y curl unzip debian-keyring debian-archive-keyring apt-transport-https && \
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg && \
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list && \
    apt-get update && apt-get install -y caddy && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Install Bun
RUN curl -fsSL https://bun.sh/install | bash
ENV PATH="/root/.bun/bin:${PATH}"

# Download go-overlay
ADD https://github.com/corebunker/go-overlay/releases/latest/download/go-overlay /go-overlay
RUN chmod +x /go-overlay

# Create directories
RUN mkdir -p /app /var/www /etc/caddy

# Copy backend
COPY backend/ /app/
WORKDIR /app
RUN bun install --production

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

2. **Install backend dependencies (local):**
   ```bash
   cd backend
   bun install
   ```

3. **Build image:**
   ```bash
   docker build -t bun-react-app .
   ```

4. **Run:**
   ```bash
   docker run -p 3000:3000 -p 4000:4000 bun-react-app
   ```

5. **Access:**
   - Frontend: http://localhost:3000
   - Backend API: http://localhost:4000/api/health

## Benchmarks (comparison with Node.js)

| Metric | Bun | Node.js 20 | Difference |
|---------|-----|------------|-----------|
| **Startup** | ~30ms | ~90ms | 🟢 3x faster |
| **Memory** | ~25MB | ~50MB | 🟢 50% less |
| **Requests/sec** | ~65k | ~45k | 🟢 44% faster |
| **Install time** | ~2s | ~15s | 🟢 7x faster |

*Approximate benchmarks for a simple REST API*

## Bun Features

### 1. Native TypeScript

No need for `tsc` or `ts-node`:

```typescript
// server.ts - runs directly!
interface User {
  id: number;
  name: string;
}

const users: User[] = [];
```

### 2. Integrated Hot Reload

```bash
bun --hot server.ts
```

### 3. Ultra-fast Package Manager

```bash
# Installs dependencies ~10x faster than npm
bun install

# Add packages
bun add express
bun add -d @types/express
```

### 4. Integrated Bundler

```bash
# Optimized build
bun build ./server.ts --outdir ./dist
```

### 5. Test Runner

```typescript
import { expect, test } from "bun:test";

test("API health", async () => {
  const res = await fetch("http://localhost:4000/api/health");
  expect(res.status).toBe(200);
});
```

## Example: backend/package.json

```json
{
  "name": "bun-backend",
  "version": "1.0.0",
  "type": "module",
  "scripts": {
    "dev": "bun --hot server.ts",
    "start": "bun run server.ts",
    "test": "bun test"
  },
  "dependencies": {
    "bun": "latest"
  }
}
```

## When to Use Bun?

### ✅ Use Bun when:
- You need maximum performance
- You want fast development with TypeScript
- You need to reduce infrastructure costs (less CPU/RAM)
- You're starting a new project
- You want all-in-one tools (bundler, test runner, package manager)

### ⚠️ Avoid Bun when:
- You need complex C++ native packages
- You use specific Node.js APIs not supported
- You need 100% compatibility with Node.js ecosystem
- Team is not familiar with new technologies

## Compatibility

Bun is compatible with most Node.js APIs:

✅ **Supported:**
- fs, path, http, https
- Buffer, Stream
- process, console
- crypto (via Web Crypto API)
- Native SQLite
- Native Fetch API
- Web APIs (Request, Response, WebSocket)

⚠️ **Partially supported:**
- Some C++ native modules
- Some specific Node.js packages

## CLI Commands

```bash
# List services
docker exec <container-id> go-overlay list

# View status
docker exec <container-id> go-overlay status

# Restart backend
docker exec <container-id> go-overlay restart bun-backend

# View logs
docker logs -f <container-id>
```

## Performance Tips

1. **Use Bun.serve() instead of Express**: Faster and less overhead
2. **Use SQLite for development**: Bun has optimized native driver
3. **Leverage the bundler**: `bun build` generates optimized bundles
4. **Use hot reload in dev**: `bun --hot` for fast development

## Migration from Node.js to Bun

Most Node.js projects work with Bun without changes:

```bash
# Simply replace node with bun
node server.js  →  bun run server.js
npm install     →  bun install
npm run dev     →  bun run dev
npx prisma      →  bunx prisma
```

## Complete Example: REST API with Validation

```typescript
// server.ts
import { Database } from "bun:sqlite";

const db = new Database("app.db");

// Schema validation helper
const validateUser = (data: any) => {
  if (!data.name || typeof data.name !== 'string') {
    return { valid: false, error: 'Name is required' };
  }
  if (!data.email || !data.email.includes('@')) {
    return { valid: false, error: 'Valid email is required' };
  }
  return { valid: true };
};

const server = Bun.serve({
  port: 4000,
  
  async fetch(req) {
    const url = new URL(req.url);
    
    // CORS headers
    const headers = {
      'Access-Control-Allow-Origin': '*',
      'Content-Type': 'application/json',
    };
    
    // GET /api/users
    if (url.pathname === '/api/users' && req.method === 'GET') {
      const users = db.query('SELECT * FROM users').all();
      return Response.json(users, { headers });
    }
    
    // POST /api/users
    if (url.pathname === '/api/users' && req.method === 'POST') {
      const body = await req.json();
      const validation = validateUser(body);
      
      if (!validation.valid) {
        return Response.json(
          { error: validation.error }, 
          { status: 400, headers }
        );
      }
      
      const result = db.query(
        'INSERT INTO users (name, email) VALUES (?, ?)'
      ).run(body.name, body.email);
      
      return Response.json(
        { id: result.lastInsertRowid, ...body },
        { status: 201, headers }
      );
    }
    
    return Response.json(
      { error: 'Not Found' }, 
      { status: 404, headers }
    );
  },
});

console.log(`🚀 Bun server running on http://localhost:${server.port}`);
```

## Features

- ✅ Native TypeScript without compilation
- ✅ 3x better performance than Node.js
- ✅ Lower memory usage
- ✅ Native integrated SQLite
- ✅ Hot reload for development
- ✅ Ultra-fast package manager
- ✅ Integrated bundler and test runner
- ✅ Compatible with Node.js APIs

## Additional Documentation

- [Bun Documentation](https://bun.sh/docs)
- [Bun API Reference](https://bun.sh/docs/api)
- [Performance Benchmarks](https://bun.sh/docs/cli/bunx)

---

**Extreme performance with modern simplicity! 🚀**
