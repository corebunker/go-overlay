# Go Overlay

Go-based service orchestrator inspired by s6-overlay for running multiple services in containers. Provides graceful shutdown, dependency management, and CLI control.

## Features

- ✅ **Auto-Installation**: Automatically installs itself in PATH when running in daemon mode
- ✅ **Graceful Shutdown**: Signal handling with configurable timeouts
- ✅ **Service State Management**: Real-time state tracking and reporting
- ✅ **Configuration Validation**: Comprehensive validation with circular dependency detection
- ✅ **CLI Commands**: Easy service management via IPC
- ✅ **Dependency Management**: Service startup ordering and waiting
- ✅ **PTY Support**: Proper log streaming with service name prefixes
- ✅ **User Switching**: Run services as different users
- ✅ **Health Monitoring**: Service failure detection and system shutdown on critical services

## Quick Start

### Docker Usage
```dockerfile
FROM alpine:latest

# Download go-overlay directly from GitHub releases
ADD https://github.com/srelabz/go-overlay/releases/latest/download/go-overlay /go-overlay
RUN chmod +x /go-overlay

# Copy your service configuration
COPY services.toml /services.toml

# Set as entrypoint
ENTRYPOINT ["/go-overlay"]
```

### Download and Install for developing new Stack
```bash
# Download latest release
curl -L https://github.com/srelabz/go-overlay/releases/latest/download/go-overlay -o go-overlay
chmod +x go-overlay

# Auto-install in PATH (creates symlink at /go-overlay)
sudo ./go-overlay install

# Now you can use from anywhere:
go-overlay list
go-overlay status
go-overlay restart nginx
```

## CLI Commands

```bash
go-overlay                    # Start daemon
go-overlay list               # List services
go-overlay status             # Show status
go-overlay restart <service>  # Restart service
go-overlay install            # Manual installation
```

## Configuration (`services.toml`)

`go-overlay` uses a `services.toml` file to define the services it should manage.

### Global Timeouts

You can specify global timeouts in a `[timeouts]` block. These are the defaults implemented in the code:

```toml
[timeouts]
post_script_timeout = 7           # Time to wait after a service starts before running its `pos_script`.
service_shutdown_timeout = 10     # Max time for a service to shut down gracefully before being killed.
global_shutdown_timeout = 30      # Max time for the entire shutdown sequence to complete.
dependency_wait_timeout = 300     # Max time to wait for a dependency to start.
```

### Service Definition

Each service is defined in a `[[services]]` block. Supported fields below reflect the current implementation:

```toml
[[services]]
name = "my-app"                             # A unique name for the service. Used for logging and CLI commands. (Required)
command = "/usr/local/bin/my-app-binary"    # The command to execute. (Required)
args = [
  "--config",
  "/etc/my-app.conf",
  "--verbose"
]                                           # A list of arguments to pass to the command. (Optional)
# log_file = "/var/log/my-app.log"          # If provided, go-overlay will tail this file instead of attaching a PTY. (Optional)
pre_script = "/scripts/setup-app.sh"        # A shell script to execute before starting the main command. (Optional)
pos_script = "/scripts/notify-startup.sh"   # A shell script to execute after the service is considered started (runs after post_script_timeout). (Optional)
depends_on = "database"                     # Name of a dependency that must be started before this service. (Optional)
wait_after = 5                              # Extra delay (in seconds) after dependency is up, before starting this service. (Optional)
enabled = true                              # If omitted, defaults to true. (Optional)
required = false                            # If true, a failure of this service triggers a graceful shutdown of all services. (Optional, default: false)
oneshot = false                             # If true, service is considered ready only after it exits successfully (ideal for migrations/jobs). (Optional, default: false)
user = "www-data"                           # Run the service as a specific user (uses `su`). (Optional)
```

## Auto-Installation

When running in daemon mode, Go Overlay automatically:
1. Detects if it's already in a PATH directory
2. Creates a symlink at `/go-overlay`
3. Enables CLI commands from any location

## Service States

- **PENDING**: Initial state, not yet started
- **STARTING**: Currently being started
- **RUNNING**: Successfully running
- **STOPPING**: Gracefully stopping
- **STOPPED**: Successfully stopped
- **FAILED**: Failed to start or crashed

## Documentation

- **[Quick Install Guide](docs/QUICK-INSTALL.md)** - Installation methods and examples
- **[CLI Commands Reference](docs/CLI-COMMANDS.md)** - Complete CLI command documentation
- **[Graceful Shutdown Testing](docs/TEST-GRACEFUL-SHUTDOWN.md)** - Testing shutdown behavior

## Examples

We provide production-ready examples with real stacks in the `examples/` directory:

### Modern Stacks

- **[FastAPI + React + Caddy](./examples/fastapi-react-stack/README.md)**: Python stack with Alembic migrations, REST API and React frontend served by Caddy
- **[Express.js + React + Caddy](./examples/express-react-stack/README.md)**: Full-stack Node.js with Prisma ORM, migrations and reverse proxy
- **[Bun + React + Caddy](./examples/bun-react-stack/README.md)**: Performance Stack with Bun runtime (3x faster than Node.js), native TypeScript
- **[Next.js Standalone](./examples/nextjs-standalone/README.md)**: Next.js in standalone mode with SSR/SSG and integrated API Routes

### Complete Production Stack

- **[Production Stack](./examples/production-stack/README.md)**: Complete stack with FastAPI + React + Redis + PostgreSQL, including:
  - Distributed cache with Redis
  - PostgreSQL database
  - Automatic migrations with Alembic
  - Health checks on all services
  - Dependency validation with `pre_script`
  - WebSocket support

### Choose Your Stack

| Stack | Ideal for |
|-------|-----------|
| **FastAPI + React** | REST APIs, Machine Learning, Python backend |
| **Express + React** | JavaScript full-stack, rapid development |
| **Bun + React** | Maximum performance, native TypeScript, reduce infrastructure costs |
| **Next.js** | Important SEO, SSR/SSG, static + dynamic pages |
| **Production Stack** | Production applications needing cache and database |

All examples include:
- ✅ Complete Dockerfiles
- ✅ Dependency configuration
- ✅ Automatic migrations
- ✅ Reverse proxy with Caddy
- ✅ Graceful shutdown
- ✅ Step-by-step instructions

## Development

This project uses `invoke` with `mise` for task management.

### Quick Start Development

```bash
# Install development tools
invoke tools

# Before committing (fast check - 20s)
invoke precommit.precommit-fast

# Before pushing to remote (complete check - 60s)
invoke precommit.precommit

# Just format code (2s)
invoke quality.fmt
```

### Common Tasks

```bash
mise exec -- invoke --list         # Lists all available tasks
mise exec -- invoke go.build       # Compiles the Go binary for your local OS
mise exec -- invoke install        # Installs the binary (uninstall with the next line)
mise exec -- invoke uninstall      # Uninstalls the installed binary
mise exec -- invoke docker.build   # Builds the Docker image
mise exec -- invoke go.test        # Runs the tests
```

## 🚀 CI/CD Pipeline

This project has a complete CI/CD pipeline with automated tests, security checks, and a release process.

### Quick Commands

```bash
mise run ci          # Full CI pipeline (tests + security + build)
mise run ci:quick    # Quick pipeline (skips security scans)
mise run cd          # CD Pipeline (release)
mise run ci:test     # Tests only
mise run ci:security # Security only
```

### Pipeline Structure

```
CI Pipeline:  Tests → Security → Build
CD Pipeline:  CI Pipeline → Release → Upload
```

**Full documentation**: [docs/CI-CD-PIPELINE.md](docs/CI-CD-PIPELINE.md)

### Automatic Execution

- **CI**: Runs on all PRs and pushes to `main`
- **CD**: Runs when creating `v*` tags or pushing to `main`

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Submit a pull request

## License

MIT License - see LICENSE file for details.
