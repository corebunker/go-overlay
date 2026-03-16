# Go Overlay

Go-based service supervisor for containers. Run multiple services with dependencies, health checks, restart policies, and graceful shutdown.

## Quick Start

```dockerfile
FROM debian:bookworm-slim

ADD https://github.com/corebunker/go-overlay/releases/latest/download/go-overlay /go-overlay
RUN chmod +x /go-overlay

COPY services.toml /services.toml
ENTRYPOINT ["/go-overlay"]
```

```toml
# services.toml
[[services]]
name = "app"
command = "/app/server"
required = true
restart = "on-failure"

[services.health_check]
endpoint = "http://localhost:8080/health"
interval = 30
```

```bash
docker build -t myapp .
docker run myapp
```

## Features

- **Dependencies**: `depends_on` + `wait_after` for startup ordering
- **One-shot Jobs**: `oneshot = true` for migrations and init scripts
- **Health Checks**: HTTP endpoint or command-based monitoring
- **Restart Policies**: `never`, `on-failure`, `always` with max attempts
- **Environment**: Inline `env`, `env_file`, runtime overrides via `GO_OVERLAY_ENABLE_*`
- **Graceful Shutdown**: SIGTERM → wait → SIGKILL with configurable timeouts
- **Pre/Post Scripts**: Run scripts before/after service lifecycle
- **User Switching**: Run services as specific users
- **CLI Management**: `list`, `status`, `restart` via IPC

## CLI

```bash
go-overlay                    # Start supervisor (reads /services.toml)
go-overlay list               # List services with status, PID, uptime
go-overlay status             # Show system summary
go-overlay restart <service>  # Restart a service
go-overlay install            # Install CLI to /usr/local/bin/
```

## Configuration

### Timeouts

```toml
[timeouts]
post_script_timeout = 7        # pos_script max duration (default: 7)
service_shutdown_timeout = 10   # SIGTERM → SIGKILL per service (default: 10)
global_shutdown_timeout = 30    # Total shutdown timeout (default: 30)
dependency_wait_timeout = 300   # Max wait for dependencies (default: 300)
```

### Service Fields

```toml
[[services]]
name = "api"                            # Required. Unique identifier
command = "/app/server"                 # Required. Executable path
args = ["--port", "8080"]               # Command arguments
enabled = true                          # Start this service (default: true)
required = false                        # Shutdown system on failure (default: false)
oneshot = false                         # Run once, ready after exit 0 (default: false)
depends_on = ["db", "redis"]            # Wait for these services
wait_after = 3                          # Seconds after deps ready (or map: { db = 5, redis = 2 })
user = "appuser"                        # Run as this user
pre_script = "/scripts/init.sh"         # Run before start
pos_script = "/scripts/cleanup.sh"      # Run after start
env = { KEY = "value" }                 # Inline env vars
env_file = "/app/.env"                  # Load from .env file
restart = "never"                       # never | on-failure | always
restart_delay = 1                       # Seconds between restarts (default: 1)
max_restarts = 0                        # 0 = unlimited (default: 0)

[services.health_check]
endpoint = "http://localhost:8080/health"  # HTTP check (2xx/3xx = healthy)
command = "pg_isready"                     # OR command check (exit 0 = healthy)
interval = 30                              # Seconds between checks (default: 30)
retries = 3                                # Failures before unhealthy (default: 3)
timeout = 5                                # Per-check timeout (default: 5)
start_delay = 10                           # Delay before first check (default: 10)
```

## Service Selection via ENV

```bash
GO_OVERLAY_ONLY_SERVICES="backend,redis" go-overlay     # Only these services
GO_OVERLAY_ENABLE_FASTAPI_BACKEND=true go-overlay        # Enable specific service
GO_OVERLAY_DISABLE_CADDY_FRONTEND=true go-overlay        # Disable specific service
```

Service names are uppercased with non-alphanumeric chars replaced by `_`.

## Complete Example

```toml
[timeouts]
service_shutdown_timeout = 10
global_shutdown_timeout = 30

[[services]]
name = "postgres"
command = "postgres"
args = ["-D", "/var/lib/postgresql/data"]
required = true

[[services]]
name = "migrate"
command = "/app/migrate"
args = ["up"]
depends_on = ["postgres"]
wait_after = 3
oneshot = true

[[services]]
name = "redis"
command = "redis-server"
required = true

[[services]]
name = "api"
command = "/app/server"
depends_on = ["postgres", "redis", "migrate"]
wait_after = { postgres = 3, redis = 1 }
required = true
restart = "on-failure"
max_restarts = 5
env = { DATABASE_URL = "postgres://localhost/app" }

[services.health_check]
endpoint = "http://localhost:8080/health"
interval = 30

[[services]]
name = "worker"
command = "/app/worker"
depends_on = ["redis"]
restart = "always"

[[services]]
name = "caddy"
command = "caddy"
args = ["run", "--config", "/etc/caddy/Caddyfile"]
depends_on = ["api"]
wait_after = 3
required = true
```

## Examples

Production-ready stacks in `examples/`:

| Stack | Use Case |
|-------|----------|
| [FastAPI + React + Caddy](./examples/fastapi-react-stack/) | Python REST APIs, ML backends |
| [Express + React + Caddy](./examples/express-react-stack/) | JavaScript full-stack |
| [Bun + React + Caddy](./examples/bun-react-stack/) | Max performance, native TypeScript |
| [Next.js Standalone](./examples/nextjs-standalone/) | SSR/SSG, SEO-focused apps |
| [Production Stack](./examples/production-stack/) | Full stack with PostgreSQL + Redis |

## Development

```bash
mise exec -- invoke go.build       # Build binary
mise exec -- invoke go.test        # Run tests
mise exec -- invoke quality.fmt    # Format code
mise exec -- invoke --list         # All tasks
```

## License

MIT License - see LICENSE file.
