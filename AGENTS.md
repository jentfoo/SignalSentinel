# Repository Guidelines

## Project Structure & Navigation
SignalSentinel is a single-binary Go app. Start at `cmd/sigsentinel/main.go`, then follow:
- `runtime.go`: app wiring, config-backed runtime methods, scan profile operations.
- `session.go`: scanner session lifecycle, reconnect logic, control intents, telemetry updates.
- `state.go`: in-process pub/sub state hub consumed by GUI and subsystems.
- `capabilities.go`: safety/availability rules for control operations.

Core internal packages:
- `internal/sds200/`: scanner protocol client (UDP control, RTSP/FTP, telemetry parsing).
- `internal/audio/ingest/`, `internal/audio/recording/`, `internal/audio/monitor/`: ingest, activity-triggered recording, and live monitoring.
- `internal/gui/`: Fyne models/views/watchers and headless test stub.
- `internal/activity/`: activity detector state machine.
- `internal/store/`: YAML config/state document loading, defaults, and persistence.

Trace key runtime flows:
- State propagation: `cmd/sigsentinel/state.go` publishes `RuntimeState`; GUI/audio subsystems subscribe through the hub.
- Control dispatch: GUI request mapping in `cmd/sigsentinel/main.go` (`mapGUIControlRequest`) routes to runtime execution and the session command queue.
- Audio path: `internal/audio/ingest/` feeds `internal/audio/recording/` and `internal/audio/monitor/`.

Build-tag navigation:
- `internal/gui/` is split by build tags (`!headless` and `headless` stub implementation).
- For fast CI-like behavior and most targeted tests, use `-tags headless`.

Config navigation:
- Default user config lives at `~/.sigsentinel/config.yaml`.
- Schema/defaults are defined in `internal/store/config.go`.
- Startup/runtime override handling is wired in `cmd/sigsentinel/runtime.go` (including flag-driven persistence flow).

Use `docs/specs-sds200.md` for device protocol behavior and `docs/specs-sigsentinel.md` for product expectations.

## Integration Guide
When adding features, wire them through the runtime path instead of patching one layer only.

New scanner control:
1. Add/extend intent and params in `cmd/sigsentinel/session.go`.
2. Enforce availability/safety in `cmd/sigsentinel/capabilities.go`.
3. Map GUI requests in `cmd/sigsentinel/main.go` (`mapGUIControlRequest` / execution path).

New telemetry or status field:
1. Parse/populate in `internal/sds200/`.
2. Extend `RuntimeState` and publish via `stateHub`.
3. Map into `gui.RuntimeState` in `main.go` and display in `internal/gui/`.

New persisted setting:
1. Add schema/defaults in `internal/store/config.go`.
2. Apply startup/runtime handling in `cmd/sigsentinel/runtime.go` and flag overrides when needed.
3. Surface in GUI settings model and save flow.

## Build, Test, and Verification Commands
- `make build`: build `./bin/sigsentinel` (includes Linux GUI dependency checks).
- `make test`: fast headless unit tests (`-short -tags headless`).
- `make test-all`: race + coverage across all packages.
- `make lint`: `golangci-lint` + `go vet`.
- `go test -tags headless -run TestName ./internal/sds200/...`: target a focused test.

Keep tests beside source files as `*_test.go`, and prefer table-driven tests for protocol/state transitions.

## Testing Conventions
Structure and naming:
- Create one `_test.go` file per implementation file that requires testing.
- Add one `func Test<FunctionName>` per target function, and use table-driven coverage via slices/maps plus `t.Run(...)` case blocks as needed.
- Keep test case names to 3-5 words, lower case, with underscores.
- Call `t.Parallel()` at the start of the test function when there is no shared mutable state; do not call `t.Parallel()` inside per-case `t.Run` blocks.
- Use `t.TempDir()` for isolated filesystem state.
- For tests that perform I/O or blocking work, derive timeout-bound contexts from `t.Context()`.

Assertions and validation:
- Use `testify` assertions (`require` for setup/preconditions, `assert` for outcome checks).
- Do not add assertion messages unless they provide context that is not obvious from the failing assertion itself.
