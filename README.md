# External IP Monitor

A standalone Go web service that checks the server's public IP immediately at startup and every 60 seconds. It serves a responsive dashboard, logs check results, and keeps a configurable in-memory history.

## Features

- Checks `https://api.ipify.org`, then falls back to `https://checkip.amazonaws.com` and `https://ipinfo.io/ip`.
- Validates returned IPv4 or IPv6 addresses.
- Logs checks, IP changes, and errors to stdout and a log file.
- Dashboard refreshes every 10 seconds and includes configurable pagination.
- JSON status endpoint at `/api/status` and health endpoint at `/healthz`.
- Graceful shutdown on SIGINT/SIGTERM.
- Uses only the Go standard library; no third-party Go modules are required.

## Requirements

- Go 1.27.2 (the current stable release used by this project) or newer to compile from source. The Go toolchain directive requests Go 1.27.2 automatically when supported.
- Outbound HTTPS access to at least one of the external IP endpoints.

## Run from source

```sh
go run .
```

Open <http://localhost:8080>.

## Compile standalone binaries

Run the build script:

```sh
./build.sh
```

It creates:

- `build/external-ip-monitor` — native executable for the current OS/architecture.
- `build/external-ip-monitor-linux-amd64` — static Linux amd64 executable.

Run the native binary:

```sh
./build/external-ip-monitor
```

Or compile manually for the current platform:

```sh
go build -trimpath -ldflags="-s -w" -o external-ip-monitor .
./external-ip-monitor
```

Compile for Linux amd64 from Linux, macOS, or Windows with Go installed:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o external-ip-monitor-linux-amd64 .
```

## Docker build and run

The build stage uses the official `golang:1.27.2-alpine` image.

Build the image:

```sh
docker build -t external-ip-monitor:latest .
```

Run it with persistent log storage:

```sh
docker run -d \\
  --name external-ip-monitor \\
  --restart unless-stopped \\
  -p 8080:8080 \\
  -v external-ip-monitor-data:/data \\
  external-ip-monitor:latest
```

Open <http://localhost:8080>. View logs with `docker logs -f external-ip-monitor`.

### Docker Compose (optional)

Save this as `compose.yaml`:

```yaml
services:
  external-ip-monitor:
    build: .
    image: external-ip-monitor:latest
    container_name: external-ip-monitor
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      IP_MONITOR_HISTORY_LIMIT: "10000"
      IP_MONITOR_PAGE_SIZES: "10,25,50,100"
      IP_MONITOR_DEFAULT_PAGE_SIZE: "25"
      IP_MONITOR_LOG_FILE: /data/ip-monitor.log
    volumes:
      - external-ip-monitor-data:/data

volumes:
  external-ip-monitor-data:
```

Then run `docker compose up -d --build`.

## Configuration

| Environment variable | Default | Description |
|---|---:|---|
| `IP_MONITOR_HISTORY_LIMIT` | `10000` | Maximum successful checks kept in memory |
| `IP_MONITOR_PAGE_SIZES` | `10,25,50,100` | Comma-separated page-size options in the dashboard |
| `IP_MONITOR_DEFAULT_PAGE_SIZE` | `25` | Initial page size; added to the options if missing |
| `IP_MONITOR_LOG_FILE` | `ip-monitor.log` | Path to the log file |

Example:

```sh
IP_MONITOR_HISTORY_LIMIT=10000 \\
IP_MONITOR_PAGE_SIZES=10,20,50,100,200 \\
IP_MONITOR_DEFAULT_PAGE_SIZE=50 \\
IP_MONITOR_LOG_FILE=./ip-monitor.log \\
go run .
```

Invalid or non-positive numeric settings fall back to defaults. History is held in memory and resets on restart; the log file persists if its directory is persistent.

## Endpoints

- `/` — dashboard.
- `/api/status` — JSON snapshot including current IP, last check, last error, and recent successful checks.
- `/healthz` — returns `ok` while the HTTP service is running.

## Notes

- The reported address is the public IP seen by the external service. Behind NAT, a VPN, or a proxy, it may be the gateway's public address rather than a local interface address.
- IP checks run once per minute. Dashboard data refreshes every 10 seconds.
- The history is intentionally in-memory; use the log file for persistent check logs.
