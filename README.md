# whereisit

[![CI](https://github.com/Boernsman/whereisit/actions/workflows/ci.yml/badge.svg)](https://github.com/Boernsman/whereisit/actions/workflows/ci.yml)
[![Docker](https://github.com/Boernsman/whereisit/actions/workflows/docker.yml/badge.svg)](https://github.com/Boernsman/whereisit/actions/workflows/docker.yml)

Where is my device? And why is Zeroconf not working? Damn it!

A lightweight service that helps you locate devices on your network. Devices register themselves with their IP address, and the server groups them by the external IP of the caller — so you only see devices from your current network.

![whereisit_ui](whereisit_ui.png)

## How it works

Devices on your network periodically POST their hostname and IP to `/api/register`. The server stores the registrations and groups them by the caller's external IP. When you open the web UI or query `/api/devices`, you see only the devices that registered from your network.

## Quick start

### Binary

```sh
./whereisit
```

### Docker

```sh
docker run -p 8180:8180 ghcr.io/boernsman/whereisit:latest
```

Override the configuration with a volume mount:

```sh
docker run -p 8180:8180 \
  -v /path/to/whereisit.ini:/etc/whereisit.ini \
  ghcr.io/boernsman/whereisit:latest
```

### Install script

```sh
curl -fsSL https://raw.githubusercontent.com/Boernsman/whereisit/main/install.sh | sh
```

## API

### Register a device

```sh
curl -X POST http://${SERVER_IP}:8180/api/register \
  -H "Content-Type: application/json" \
  -d '{"name":"${DEVICE_NAME}","address":"${DEVICE_IP}"}'
```

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Human-readable device name |
| `address` | string | Device IP address |
| `id` | string | Unique identifier (e.g. serial number) — used to update existing entries |
| `tags` | object | Arbitrary key/value metadata |

### List devices (current network)

```
GET http://${SERVER_IP}:8180/api/devices
```

Returns only devices that registered from the same external IP as the caller.

### List all devices

```
GET http://${SERVER_IP}:8180/api/alldevices
```

### Web UI

```
http://${SERVER_IP}:8180
```

## Configuration

The server reads config from `/etc/whereisit.ini`, falling back to `./whereisit.ini`.

```ini
[basic_auth]
enabled  = false
username = admin
password = admin

[api]
api_key_enabled = false
api_key         = your_api_key
```

### Command-line flags

| Flag | Default | Description |
|------|---------|-------------|
| `--http-port` | `8180` | HTTP listen port |
| `--public` | `./public/` | Path to static web files |
| `--lifetime` | `24` | Device entry lifetime in hours |
| `--verbose` | `false` | Enable debug logging |

## Security

Both authentication methods are disabled by default and configured in `whereisit.ini`.

**Basic Authentication** — enables username/password protection for the API. Use a TLS-terminating reverse proxy to protect credentials in transit.

**API Key Authentication** — clients include an `X-API-Key` header with every request.

## Client examples

Ready-to-use registration scripts are in [`examples/`](examples/):

- [`examples/sh/`](examples/sh/) — shell scripts
- [`examples/python/`](examples/python/) — Python clients
- [`examples/systemd/`](examples/systemd/) — systemd timer for periodic registration

## Build

```sh
go build .
```

## Test

```sh
go test .
```

## License

[MIT](https://tldrlegal.com/license/mit-license)
