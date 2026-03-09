# SignalSentinel v1 Specification (SDS200 Control + Monitoring)

## 1. Purpose

SignalSentinel is a single-binary Go desktop application for Uniden SDS200 scanners.

v1 goals:

1. Connect to one SDS200 over Ethernet.
2. Control scanner functions needed for monitoring workflows.
3. Ingest scanner telemetry and audio in near real-time.
4. Detect traffic activity and record complete conversation windows.
5. Provide a native cross-platform GUI for configuration, monitoring, and playback.
6. Persist recordings and metadata locally.
7. Provide operator-first usability controls for scan navigation, quick-key/service-type scope control, recording management, live audio monitoring, and expert operations.

---

## 2. Product Decisions (Locked)

1. UI: native GUI only, implemented with Fyne.
2. Service model: single process, single binary.
3. Channel/state authority: scanner is primary source of scan state; app queries and overlays.
4. Command conflict resolution: last-write-wins.
5. Recording segmentation: one file per active transmission window with fixed 10s hang-time.
6. Error policy: any subsystem hard fault is fatal; application exits after logging reason.
7. Test strategy: hardware-in-loop scenario checklist using real SDS200.
8. Recordings browser/playback in v1 is local-only.
9. Scan lifecycle UX must reflect actual SDS200 command semantics (hold/navigation driven), not synthetic start/stop abstractions.
10. Quick-key and service-type controls are first-class power-user features in v1.
11. Advanced scanner operations ship in an expert surface with safety gating and confirmations.
12. Recordings catalog updates are automatic during runtime; normal operation must not require a manual refresh action.

---

## 3. Runtime and Deployment

### 3.1 Runtime

Language/runtime:

1. Go (current stable toolchain).
2. CGO allowed if needed by GUI/audio dependencies.

### 3.2 Launch Model

v1 launch command:

```bash
sigsentinel [flags]
```

Supported flags:

1. `--config <path>`: path to config YAML (default `~/.sigsentinel/config.yaml`).
2. `--scanner-ip <ip>`: set/persist `config.scanner.ip` before runtime startup.
3. `--recordings-path <path>`: set/persist `config.storage.recordings_path` before runtime startup.
4. `--help`: print usage and exit without starting runtime services.

Behavior:

1. Parse CLI flags.
2. If `--help` is provided, print usage and exit.
3. If `--scanner-ip` and/or `--recordings-path` are provided, load config, apply defaults, persist overrides, and validate.
4. Start internal services.
5. Open native GUI.
6. Run until user exits or fatal fault occurs.

No daemon/headless mode in v1.

### 3.3 Platforms

Target support:

1. Linux (primary validation platform).
2. macOS.
3. Windows.

---

## 4. High-Level Architecture

All modules run in one process:

```text
sigsentinel
├── app_orchestrator
├── config_manager
├── sds200 (protocol + transport + telemetry)
├── audio_engine (RTSP/RTP ingest + decode)
├── activity_detector
├── recording_manager
├── yaml_store (user config + app-specific persisted metadata)
└── fyne_gui
```

Key rule: every scanner-facing action flows through the `sds200` command path to enforce serialization, retries, and state validation.

---

## 5. SDS200 Connectivity and Protocol

### 5.1 Scanner Network Services

1. UDP control/telemetry: port 50536.
2. RTSP control: port 554.
3. RTP audio stream: negotiated from RTSP session.
4. FTP is out of scope for v1.

### 5.2 UDP Command Protocol

Framing:

1. ASCII commands terminated by carriage return (`\r`).
2. Response format begins with command token.
3. Some responses are XML and may be fragmented across packets.

Representative commands used in v1:

1. `MDL`, `VER` for capability/identity checks.
2. `PSI` for push telemetry enable.
3. `STS`/`GSI`/`GLT`/`GST` as needed for status/list retrieval.
4. `KEY`, `HLD`, `NXT`, `PRV`, `JNT`, `QSH`, `JPM` for operator scan/navigation actions.
5. `FQK`/`SQK`/`DQK`/`SVC` for scan-scope control.
6. `VOL`, `SQL`, `AVD`, `URC` for operational controls.
7. Expert-surface commands (`MNU`/`MSI`/`MSV`/`MSB`, `AST`/`APR`/`PWF`/`GWF`, `DTM`, `LCR`, `GCS`, `POF`) with explicit guardrails.

### 5.3 Command Reliability Model (Stateful, Robust)

Requirements:

1. All outbound commands enter a single ordered command queue.
2. Each command has timeout, retry policy, and completion state.
3. Multi-packet XML responses must be reassembled using packet footer sequence/EOT.
4. Missing fragments trigger full command retry.
5. Responses must be validated against expected command type.
6. Unknown/malformed responses are logged and treated as command failure.
7. After reconnect, app performs state resync before accepting new control actions.

Behavioral policy:

1. Retries are finite.
2. Exhausting retries for required control path is a hard fault.
3. Last-write-wins applies to conflicting user actions enqueued close together.

---

## 6. Telemetry Model

### 6.1 Push-First Operation

Primary telemetry mode:

1. Enable scanner push telemetry at startup.
2. Consume status updates continuously.

Fallback:

1. Poll status only for recovery/resync or when push stream is unavailable briefly.

### 6.2 Canonical Runtime Status

App must maintain a canonical in-memory status snapshot including:

1. Connection state.
2. Current frequency/talkgroup context.
3. System/department/channel labels (as available).
4. Hold/scan mode state.
5. Signal/squelch indicators.
6. Last update timestamp.

GUI binds to this snapshot and updates in near real-time.

---

## 7. Audio Ingest and Processing

### 7.1 Audio Source

RTSP source:

```text
rtsp://<scanner-ip>:554/au:scanner.au
```

Expected format:

1. G.711 u-law.
2. 8000 Hz sample rate.

### 7.2 Audio Pipeline

```text
RTSP session -> RTP packet ingest -> G.711 decode -> PCM frame stream
```

Pipeline requirements:

1. Continuous ingest while connected.
2. Timestamp frames for accurate clip boundaries.
3. Deliver frames to activity detector and recorder with bounded buffering.

---

## 8. Activity Detection and Recording Windows

### 8.1 v1 Detection Inputs

Primary trigger source:

1. Scanner telemetry (squelch/signal status and channel activity cues).

Audio-level heuristics may be used only as supplemental guardrails, not as sole trigger in v1.

### 8.2 Recording State Machine

State transitions:

1. `IDLE` -> `ACTIVE` when telemetry indicates traffic start/open squelch.
2. Remain `ACTIVE` while traffic persists.
3. On traffic end/squelch close, enter `HANG`.
4. `HANG` duration is fixed at 10 seconds.
5. If activity resumes during `HANG`, return to `ACTIVE` and continue same recording.
6. If no activity during `HANG`, finalize recording and return to `IDLE`.

Outcome: related call-and-response traffic is grouped into one clip when gaps are <=10s.

### 8.3 False-Positive Suppression and Activity Quality

To reduce short false-positive activity events:

1. Activity start must pass a debounce threshold (`config.activity.start_debounce_ms`, default 300ms).
2. Activity end must pass a debounce threshold (`config.activity.end_debounce_ms`, default 800ms).
3. Very short windows shorter than `config.activity.min_activity_ms` (default 1200ms) are marked as suppressed activity unless a recording file was actually written.
4. Suppressed activity entries are visible in logs/diagnostics with suppression reason but are hidden by default in the main activity list.
5. Activity quality logic must remain telemetry-first and may use audio energy as a secondary guardrail only.

---

## 9. Recording Manager

### 9.1 File Format

Default and required format:

1. FLAC.

WAV may be optionally supported later, but is not required for v1.

### 9.2 Naming

Filename pattern:

```text
YYYYMMDD_HHMMSS_frequency_system_channel.flac
```

Rules:

1. Timestamp is local time at recording start.
2. Invalid filename characters in labels are sanitized.
3. If labels are unknown, use stable placeholders (`unknown_system`, etc.).

### 9.3 Metadata Captured per Recording

Minimum metadata fields:

1. Recording ID (internal UUID or integer key).
2. Start/end timestamps.
3. Duration.
4. Frequency/talkgroup (if available).
5. System and channel labels (if available).
6. File path.
7. File size.
8. Trigger type (`telemetry`, `manual`, or `mixed`).

### 9.4 Finalization Guarantees

1. File write must be atomic from user perspective (no half-visible incomplete files in normal flow).
2. Metadata is written only after successful file finalization.
3. Failure to write/finalize is a hard fault and causes process exit.

### 9.5 Manual Recording Control

Requirements:

1. Operator can start and stop recording manually from the GUI regardless of current activity detection state.
2. Manual start creates an active recording session immediately and uses the same audio ingest path as telemetry-triggered recording.
3. Manual recording stop finalizes the clip immediately after buffered frames are flushed.
4. If telemetry activity also occurs during a manual session, the recording trigger type is set to `mixed`.
5. Manual controls must show explicit state (`idle`, `recording`) and actionability in the GUI.

### 9.6 Recording Deletion and Catalog Management

Requirements:

1. Recordings view must support deleting one or multiple selected recordings.
2. Deletion removes both catalog metadata and local file by default.
3. UI must show a confirmation dialog with selection count before destructive delete.
4. Missing/unreadable files must not block metadata cleanup; partial outcomes must be shown as warnings.
5. Deletion failures are non-fatal and visible in the recordings panel and logs.
6. Recordings view must include an `Open Folder` action to reach the storage directory quickly.
7. Recordings catalog updates must appear automatically while the app is running; manual refresh is not required for normal workflows.

---

## 10. Scanner Configuration and Control

### 10.1 Source of Truth

Scanner favorites and internal scanner state are authoritative.

App responsibilities:

1. Query scanner lists/state.
2. Present and filter scanner-derived data in GUI.
3. Apply user-selected control actions through scanner commands.

### 10.2 App Overlay Channels

v1 may support lightweight app-defined quick entries (for temporary monitoring), but must not replace scanner’s persistent programming model.

### 10.3 Control Modes

v1 usability baseline supports:

1. Hold/release hold actions with explicit feedback of resulting scanner mode.
2. Next/previous scan navigation actions.
3. Jump actions (number tag, quick search hold, jump mode) when scanner context supports them.
4. Selected direct key-driven scanner actions (`KEY`) where command mapping is validated.
5. Optional advanced workflows only when implementable through reliable SDS200 command patterns.

If a mode cannot be controlled reliably via command protocol, it must be omitted from v1 UI rather than partially implemented.

### 10.4 Channel Group Selection and Scan Scope

Requirements:

1. App must load scanner-derived scan-scope controls as first-class surfaces: Favorites/System/Department quick keys and Service Types.
2. Operator can include/exclude scan scope at runtime by editing quick keys and service types.
3. Scope-change actions are serialized through the same control queue and include success/failure feedback.
4. Current effective scan scope is visible in the GUI.
5. App may persist named scope profiles in `state.scan_profiles` for fast recall.
6. Power-user ergonomics are required: bulk select/deselect, staged apply/preview, and fast filtering.

### 10.5 Extended Scanner Operations

The GUI must expose a scanner operations area for high-frequency controls beyond hold/resume, including:

1. Volume adjust.
2. Squelch adjust.
3. Temporary avoid/unavoid current channel/talkgroup when scanner protocol supports it reliably.
4. Scanner command/status feedback panel that shows last command result and failure reason.
5. Capability-gated action availability and disabled-reason messaging.
6. Monitor mute is a local playback control and must not require a dedicated scanner mute command.

### 10.6 Advanced Expert Operations

Advanced SDS200 capabilities are exposed in an expert panel with strict guardrails:

1. Menu navigation and value operations (`MNU`, `MSI`, `MSV`, `MSB`).
2. Analyze and waterfall workflows (`AST`, `APR`, `PWF`, `GWF`) when hardware behavior is validated.
3. Device time/location operations (`DTM`, `LCR`) with input validation.
4. Device health/identity operations (`MDL`, `VER`, `GCS`, keepalive status).
5. Power-off (`POF`) requires elevated confirmation and audit logging.
6. Expert operations must not interfere with primary monitoring/recording flows without explicit operator action.

---

## 11. GUI Specification (Fyne)

### 11.1 Main Areas

1. Connection and scanner status.
2. Live now-playing/status panel (frequency, system, channel, signal/squelch).
3. Activity timeline/list with compact per-session rows (active and recent transmissions).
4. Scan control actions (hold/release, next/previous, jump actions, validated key actions).
5. Power scan-scope panel for quick keys/service types and profile recall.
6. Recordings browser and local playback/delete controls.
7. Live audio monitor controls (listen, mute, monitor gain/output device).
8. Scanner operations panel (volume, squelch, avoid, action feedback).
9. Expert operations panel (menu/analyze/device tools), gated behind explicit enablement and confirmation.
10. Settings panel (scanner IP, storage path, startup behavior, scanner tuning options, expert-mode toggle).

### 11.2 UX Behavior

1. Connection state is always visible.
2. Fault states are explicit and include actionable reason text.
3. Long operations (startup sync/reconnect) show progress indicators.
4. User controls that are unavailable in current state are visibly disabled.
5. Recordings catalog load failures are shown in the recordings panel without clearing the last successfully loaded list.
6. Recordings list updates continuously during normal operation; stale list state must self-correct without user refresh actions.
7. Disabled controls include tooltip/help text stating why action is unavailable.
8. Activity rows are compact and should append end/duration to the original start row instead of creating a second event row.
9. Recordings delete actions require explicit confirmation and show post-action summary (`deleted`, `missing`, `failed` counts).
10. Scan state labels are explicit (`Scanning`, `Hold`, `Paused`, `Disconnected`) and must stay in sync with canonical runtime state.
11. Recordings playback scope is local-only in v1; scanner-hosted browse/retrieve is not exposed.
12. Expert operations are clearly segmented from primary controls and require explicit confirmation for risky actions.

### 11.3 Concurrency and Arbitration

1. UI actions enqueue control intents.
2. Backend command queue executes intents in order.
3. Last-write-wins when user issues conflicting actions rapidly.

### 11.4 Activity Log Presentation Contract

1. Each activity session is represented by one row containing start time, end time, duration, channel context, and recording outcome.
2. While session is active, the row is updated in place (no duplicate trailing row).
3. UI includes density controls (`compact`, `detailed`) to reduce vertical space usage.
4. UI supports optional display of suppressed/filtered activity events for debugging.

---

## 12. Local Config Store

### 12.1 Engine

1. YAML file persisted on local disk.

### 12.2 Required Sections

1. `config`: user-managed settings loaded at startup.
2. `state`: app-managed persisted metadata (not live scanner state).
3. `state.recordings`: clip metadata and file pointers.
4. `state.favorites`: app-level operator favorites/shortcuts.
5. `state.scan_profiles`: optional named channel-group scan selections.

### 12.3 Data Integrity

1. Config schema version must be explicit and checked.
2. On startup, schema compatibility must be checked before normal operation.
3. YAML read/write failure is a hard fault.

---

## 13. Configuration

### 13.1 Location

1. Default path:

```text
~/.sigsentinel/config.yaml
```

2. `--config <path>` overrides the config file location for that process launch.

### 13.2 Required Fields

1. `config.scanner.ip`
2. `config.scanner.control_port` (default 50536)
3. `config.scanner.rtsp_port` (default 554)
4. `config.storage.recordings_path`
5. `config.recording.hang_time_seconds` (fixed default 10, configurable only if explicitly changed)
6. `config.activity.start_debounce_ms` (default 300)
7. `config.activity.end_debounce_ms` (default 800)
8. `config.activity.min_activity_ms` (default 1200)
9. `config.audio_monitor.default_enabled` (default false)
10. `config.audio_monitor.output_device` (default system default)
11. `config.audio_monitor.gain_db` (default 0)
12. `state.favorites` (list)
13. `state.recordings` (list)
14. `state.scan_profiles` (list, may be empty)
15. `config.ui.expert_mode_enabled` (default false)

### 13.3 Startup Validation

On launch, validate:

1. Config YAML parseability.
2. Required fields present.
3. Scanner IP syntactically valid.

Validation failure is a startup hard fault.

### 13.4 Runtime Apply Behavior

1. Saving `config.storage.recordings_path` applies immediately for newly created recordings after the save succeeds.
2. Saving scanner connection settings (for example `config.scanner.ip`) persists immediately but requires app restart to reinitialize scanner/audio sessions with the new endpoint.
3. Saving activity suppression settings applies to newly detected activity windows without app restart.
4. Saving audio monitor output/gain settings applies immediately when live monitor is active.
5. Saving expert-mode toggle applies immediately to show/hide expert operations panel.

---

## 14. Logging and Observability

### 14.1 Log Content

Logs must include:

1. Scanner connect/disconnect/reconnect lifecycle.
2. Command send/response failures and retry outcomes.
3. Telemetry parse anomalies.
4. Recording start/stop/finalize events.
5. Manual recording start/stop operations and resulting trigger mode.
6. Recording delete actions (requested count, deleted count, failures).
7. Activity suppression decisions and reasons for filtered short events.
8. Scanner command action/result summaries for operator-initiated controls.
9. Fatal fault cause and subsystem.
10. Scope-apply operations (quick key/service type/profile) with requested/applied/failed counts.
11. Expert operation executions and confirmations (including power-off intent).

### 14.2 Log Format

1. Standard library `log` output is acceptable in v1.
2. Local timestamp with timezone.
3. Log level field (`DEBUG`, `INFO`, `WARN`, `ERROR`, `FATAL`).

---

## 15. Error Handling Policy (Fail-Fast)

### 15.1 Hard Fault Definition

Any unrecoverable subsystem fault is hard and must terminate the process. This includes:

1. Persistent scanner control failure after retry budget.
2. RTSP/audio pipeline unrecoverable failure.
3. Recorder write/finalization failure (including disk full).
4. Runtime YAML read/write failure.
5. Internal state corruption or invariant violation.

Non-fatal operational errors:

1. User-initiated recording deletion failure.
2. Unsupported optional scanner operation (for example avoid/unavoid on unsupported mode).
3. UI-level audio monitor start failure while recording subsystem remains healthy.
4. Unsupported expert operation in current scanner mode/state when capability gating is functioning.

### 15.2 Shutdown Behavior on Hard Fault

1. Emit fatal log entry with root cause context.
2. Attempt orderly stop of active routines for short bounded timeout.
3. Exit process with non-zero status.

No degraded "keep running partially broken" mode in v1.

---

## 16. Security Model

1. Scanner protocol is unencrypted and unauthenticated.
2. App is intended for trusted local networks.
3. Remote access to the host should occur only through user-managed secure channels (for example VPN/SSH), outside app scope.
4. Since v1 has no API server, there is no application HTTP attack surface.

---

## 17. Performance and Resource Targets

Expected baseline on typical desktop/laptop:

1. CPU: low single-digit to low double-digit percentages during normal monitoring.
2. Memory: target <200 MB steady-state.
3. Storage: proportional to recording volume; no auto-pruning in v1.

---

## 18. Packaging and Distribution

1. Primary artifact: single executable per platform.
2. Linux packaging may include AppImage as optional distribution format.
3. Installer/service wrappers are out of scope for v1.

---

## 19. Hardware-in-Loop Acceptance Checklist (v1 Gate)

A v1 build is acceptable only after passing this checklist on a real SDS200:

1. Connect/disconnect: app connects to scanner and reflects live status.
2. Startup sync: model/version/status retrieval succeeds after launch.
3. Telemetry: push updates flow continuously during normal scanning.
4. Control actions: hold/resume/navigation actions execute and reflect expected scanner behavior.
5. Recording start/stop: active traffic produces recordings with expected metadata.
6. Hang-time grouping: gaps <=10s remain in one recording; >10s create new recording.
7. Audio validity: saved FLAC files are playable and intelligible.
8. Reconnect recovery: temporary network interruption recovers without manual app restart when fault is recoverable.
9. Hard fault handling: induced unrecoverable fault produces fatal log and process exits non-zero.
10. Cross-platform smoke tests: basic launch and scanner monitoring on Linux, macOS, and Windows.
11. Manual recording: operator can start/stop clip and clip is finalized with `manual` or `mixed` trigger metadata.
12. Recordings deletion: single and batch delete remove files/metadata correctly with confirmation UX.
13. Recordings catalog freshness: recordings list stays synchronized during normal operation without requiring manual refresh.
14. Activity compaction: a call session appears as one row with start+end/duration fields and no duplicate end row.
15. False-positive suppression: short transient telemetry events are filtered and counted without polluting main activity list.
16. Scan lifecycle controls: hold/release, navigation, and jump actions are actionable and reflect actual scanner mode.
17. Scan-scope controls: quick keys and service types can be edited reliably and scanner behavior reflects selected scope.
18. Extended scanner controls: volume/squelch/avoid actions execute and update UI status in near real-time.
19. Live audio monitoring: operator can listen to scanner audio stream locally with controllable monitor state.
20. Expert operations: enabled advanced panel actions are capability-gated, confirmed when risky, and produce auditable command results.
