# Container Runtime Observability Improvements Design

## Goal

Improve the Containers module so AIFAR Runtime service state updates without manual refresh, runtime errors are easy to identify with their stack context, and SSH terminal output remains visible and scrollable regardless of output volume.

## Scope

This change covers three existing frontend flows and their current backend event source:

1. AIFAR Runtime service status under Containers.
2. AIFAR Runtime log streaming and filtering.
3. The SSH Terminal page layout and scrollback behavior.

It does not introduce a second realtime protocol, change Docker collection semantics, alter task execution, or redesign unrelated Containers tabs.

## Current Causes

### Runtime status

The backend collector runs at `AIFAR_COLLECTOR_INTERVAL_SECONDS=15` and publishes every `aifar.runtime` snapshot through the global SSE stream. Snapshot events include a `changed` flag. `ContainersView.vue` currently handles `docker.summary` events by merging them into visible state, but handles `aifar.runtime` events only by clearing its cache. The displayed runtime response therefore remains unchanged until the user refreshes or changes page state.

### Runtime logs

The current parser recognizes a narrow timestamp-first, single-line format. Stack frames and lines such as `Caused by`, exception class names, and nested error details have no explicit log level, so they are displayed and filtered as ordinary unclassified rows instead of part of the preceding error.

### Terminal layout

The terminal card uses a viewport-based fixed height while its ancestors also reserve dynamic space for page headers and global status components. The ancestor content region clips overflow. When those dynamic regions consume more space than the fixed subtraction expects, the bottom of the terminal is outside the visible content area.

## Chosen Architecture

### 1. Event-driven runtime refresh

Keep the existing authenticated global SSE connection and collector snapshots. When the selected server receives an `aifar.runtime` event while the Runtime workspace is active, schedule a forced runtime detail reload.

The scheduler will coalesce events received during the same short window and prevent overlapping detail requests. If another applicable event arrives while a request is running, exactly one follow-up request will run after the current request completes. This preserves the backend-driven 15-second cadence, reflects changed snapshots without manual refresh, and avoids a separate page-specific SSE connection.

The existing manual refresh action remains available as an explicit recovery and diagnostics control.

### 2. Stateful error classification

Extend runtime log classification to recognize:

- ISO timestamps and `yyyy-MM-dd HH:mm:ss.SSS`-style timestamps.
- Plain and bracketed `TRACE`, `DEBUG`, `INFO`, `WARN`, `ERROR`, `FATAL`, and `SEVERE` levels.
- Structured JSON log records with common `level`, `severity`, `message`, and timestamp fields.
- Exception markers such as `Exception`, `Error`, `Caused by`, `Suppressed`, and Java stack frames.

Classification will retain per-pod error context while rows are converted. Stack frames and nested-cause lines following an error inherit the `ERROR` level until a new timestamped or explicitly levelled record begins. This keeps a complete exception visible when the user filters to errors.

The log toolbar will add a localized one-click error-only control and an error row count. Error rows will use a subtle semantic error background and border while retaining the current virtualized list, keyword filter, service filter, pod filter, pause, and auto-scroll behavior.

### 3. Flex-based terminal sizing

Use the content area's existing flex contract instead of a viewport subtraction:

- The terminal page will keep overflow contained on desktop.
- The terminal card will flex into the remaining page space with a practical minimum height.
- The terminal box and xterm viewport will own vertical scrolling.
- xterm scrollback will be explicitly increased so older output can be reached after large commands.
- Mobile layouts will retain a bounded terminal height and natural page scrolling.

## Data Flow

1. The backend collector emits its normal status snapshot event every collection cycle.
2. The realtime Pinia store records the snapshot and increments its event revision.
3. `ContainersView.vue` recognizes an event for the currently selected server and active Runtime workspace.
4. A coalescing scheduler fetches the current Runtime response and replaces visible service, deployment, pod, ingress, and agent state.
5. Log streaming remains independent and continues to use its existing runtime log SSE endpoint.

## Error Handling

- A failed automatic runtime refresh keeps the last usable runtime state and exposes the existing page error message; later events may retry.
- Automatic refreshes never run concurrently for the same view.
- Malformed JSON log lines fall back to text parsing and remain visible.
- Unclassified log lines remain available under the existing no-level behavior unless they are confirmed continuation lines of an error.
- Terminal disconnect and WebSocket error behavior remains unchanged.

## Testing

Add focused tests before implementation:

1. Runtime refresh scheduler tests for event coalescing, in-flight follow-up behavior, and disposal.
2. Runtime log tests for common timestamps, bracketed levels, structured JSON, exception detection, and multiline error inheritance without leaking error state into the next normal record.
3. A terminal layout contract test covering flex sizing, contained overflow, xterm viewport scrolling, and configured scrollback.

Then run:

- Focused Vitest files during red-green development.
- `pnpm test:web` for all frontend logic tests.
- `pnpm web:build` for Vue and TypeScript validation plus the production build.
- Relevant backend collector tests to ensure the existing 15-second snapshot contract remains intact.
- `git diff --check` for whitespace validation.

## Non-Goals

- Replacing global SSE with WebSocket.
- Sending the complete Runtime response in every global snapshot event.
- Adding browser-side 15-second polling.
- Changing the runtime log transport or Docker log collection implementation.
- Redesigning server management or task logs.
