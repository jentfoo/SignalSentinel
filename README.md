# SignalSentinel
SignalSentinel is a single-binary Go desktop app (Fyne GUI) for monitoring and controlling a Uniden SDS200 over Ethernet. It ingests scanner telemetry + audio, detects traffic windows, and stores local FLAC recordings with metadata.

## Requirements
- Uniden SDS200 reachable on your local network
- Go `1.25.5+`
- Linux GUI build deps (Debian/Ubuntu):
  ```bash
  sudo apt-get update
  sudo apt-get install -y pkg-config libgl1-mesa-dev xorg-dev
  ```

## Build
```bash
make build
```

Binary output:
- `./bin/sigsentinel`

## Configuration
Default config path: `~/.sigsentinel/config.yaml` (override with `--config`).

## Run
```bash
./bin/sigsentinel --config /tmp/sigsentinel.yaml --scanner-ip 192.168.1.118 --recordings-path /tmp/recordings
```

Useful flags:
- `--config <path>` config YAML path
- `--scanner-ip <ip>` persist scanner IP before startup
- `--recordings-path <path>` persist recordings path before startup
- `--help` print CLI help
