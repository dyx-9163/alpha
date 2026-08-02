# Server Status Backend Push Design

Date: 2026-08-03

Status: Approved in conversation

## Problem

The Servers page currently calls `probeAllOnce()` after loading. Every page entry therefore creates one `servers.probe` worker task per server, waits for those tasks, and reloads the list. This couples a read-only page visit to remote SSH work, task creation, and audit noise.

The control plane already has a global SSE stream, persisted status snapshots, and a collector that runs every `AIFAR_COLLECTOR_INTERVAL_SECONDS` (15 seconds by default). However, `collector.Manager.collectServers()` only copies the persisted `servers.status` value into a `server` snapshot; it does not perform a live SSH check. The page therefore cannot rely on backend-driven server status today.

## Goals

- Move routine server reachability checks from page entry to the backend collector.
- Check every registered server every 15 seconds by default.
- Push live server status through the existing global SSE stream.
- Make page entry and list refresh read-only operations that never create probe tasks.
- Preserve the manual `servers.probe` workflow, including permission checks, worker steps, logs, cancellation behavior, and audit records.
- Keep credentials out of snapshots, events, logs, and frontend state.
- Bound probe latency and concurrency so one slow or unreachable server cannot block the remaining servers.

## Non-goals

- Replacing the existing global SSE transport.
- Adding a new API endpoint, database migration, scheduler, or configuration key.
- Running inventory, telemetry, Docker, or arbitrary remote commands as part of the routine server check.
- Removing the manual Probe Host action.
- Turning routine background checks into worker tasks or audit records.
- Adding failure hysteresis, alert policy changes, or a new stale-status badge in this change.

## Chosen Approach

Extend the existing collector. Its `servers` collection stage will perform a lightweight SSH connection and authentication check, persist the result as a `server` status snapshot, and publish `status.server.updated` through the existing realtime hub when the effective status changes.

This is preferred over periodic `servers.probe` tasks because a 15-second task schedule would continuously create tasks, steps, logs, and audit entries. A separate server-monitoring service would duplicate the existing collector lifecycle, scheduling, snapshot, and realtime infrastructure without a current isolation requirement.

## Backend Design

### Probe dependency

`collector.Manager` receives an injectable server probe function with the contract:

```go
func(context.Context, store.Server) error
```

Production wiring uses the existing lightweight SSH probe implementation. Tests inject fake probes and never contact real servers.

The manager owns these fixed operational limits:

- Per-server timeout: 5 seconds.
- Maximum concurrent server probes: 8.
- Schedule: the existing collector interval, 15 seconds by default.

No new configuration is introduced in this change. The limits can be promoted to configuration later if deployment evidence demonstrates a need.

### Collection algorithm

For each `collectServers()` run:

1. Read the public server list to establish stable targets and ordering.
2. Process targets through an eight-worker bounded pool.
3. Load each target again with decrypted credentials immediately before probing.
4. Create a five-second child context for that server.
5. Attempt SSH connection and authentication only; do not create a remote session or execute a command.
6. Convert success to `available` with an empty `lastError`.
7. Convert missing credentials, authentication failure, network failure, and timeout to `failed` with a masked `lastError`.
8. Persist a `server` status snapshot containing only public server metadata and the probe result.

An unreachable server is an observed resource state, not a collector infrastructure failure. It must not stop other probes or mark the entire `servers` collector run failed. Database read/write errors, parent-context cancellation, and internal coordination failures remain collector failures.

### Snapshot and event behavior

The background collector does not update the canonical `servers` row. Live monitoring state remains in `status_snapshots(scope='server')`; server configuration and the manual probe's persisted result remain in the existing server record.

Each server snapshot contains:

- `scope`: `server`
- `resourceId` and `serverId`: server ID
- `status`: `available` or `failed`
- `lastError`: masked error text or empty
- `payload`: public ID, name, host, Docker host, status, and server update time
- `collectedAt`, `updatedAt`, and monotonic snapshot `version`

The snapshot is refreshed on every collection so reconnecting clients can read a current `collectedAt`. For the `server` scope, the hub publishes `status.server.updated` only when status-relevant snapshot content changes. Existing publish-on-collection behavior for other scopes remains unchanged because Runtime and other views use those events for detail refresh.

Routine collection does not publish a `probing` state. This avoids flashing all servers every 15 seconds.

### Manual probe interaction

The manual `POST /servers/{id}/probe` path is unchanged. It continues to create a `servers.probe` task with steps and audit output. The frontend continues to show local `probing` state, waits for task completion, and reloads the canonical server list immediately. The next background collection reconciles that result into the server snapshot without creating another task.

If a manual probe and background probe overlap, the later completed observation becomes the latest snapshot. Snapshot version and timestamps prevent an older frontend event or in-flight GET from overwriting a newer observation.

## Frontend Design

### Page lifecycle

`ServersView.vue` will load server defaults and the server list on mount. It will no longer call `probeAllOnce()`. The Refresh List action continues to reload server configuration only.

Removing the automatic call also removes the obsolete one-shot automatic probe state and helper if no other caller remains. The manual `probe(row)` action remains.

### Realtime snapshot access

The realtime store adds a `serverSnapshot(serverId)` getter consistent with the existing application-instance and Docker snapshot getters.

A pure server-status merge helper applies a valid `server` snapshot to a `ServerRecord`. It may replace only:

- `status`
- `lastError`

It must preserve server identity, connection metadata, tags, note, deploy directory, sort order, and all unrelated fields.

The server workbench applies snapshots:

- after the initial server-list request;
- after the realtime snapshot cache initially loads; and
- whenever `realtime.statusRevision` advances.

Updates replace only the matching item in the current array. They do not issue another list request, reset `selectedId`, clear search text, reorder servers, close the edit drawer, or change the active detail tab.

### Status precedence

For display, a valid live `server` snapshot takes precedence over the canonical server row. Without a snapshot, the existing row status remains the fallback. While a user-triggered manual probe is active, local `probingIds` takes visual precedence for that server until the task finishes.

The realtime store's existing version and timestamp freshness comparison remains authoritative. An older snapshot or older in-flight status-snapshot GET cannot replace a newer SSE observation.

SSE disconnection leaves the last accepted status visible. On reconnection, the existing `/status/snapshots` reload supplies the latest persisted server snapshots.

## Security and Privacy

- Decrypted SSH credentials exist only in backend memory for the duration of each probe.
- Snapshot payloads use public server data and never include password or private-key fields.
- Errors pass through the existing masking boundary before persistence or publication.
- Automated tests assert that fixture secrets do not appear in snapshots or realtime events.
- The collector executes no user-supplied or free-form shell command.

## Error Handling

- Per-server timeout or probe error: save that server as `failed`; continue the batch.
- Missing or undecryptable credential: save a masked `failed` result; continue the batch.
- Failure loading one encrypted server record: save a public failed snapshot for that target when possible; continue the batch.
- Database snapshot write failure: collect the error, continue already-started independent targets, and return a combined collector error after workers finish.
- Parent context cancellation: stop scheduling new work, allow workers to observe cancellation, and return the context error.
- SSE delivery failure: no effect on collection; clients recover from persisted snapshots after reconnecting.

## Testing

### Backend

Add collector tests that prove:

- a successful fake SSH probe writes an `available` server snapshot;
- authentication, connection, missing-credential, and timeout failures write masked `failed` snapshots;
- at most eight probes run concurrently;
- a slow probe times out without blocking a fast target;
- one unreachable server does not make the whole collector run fail;
- database/persistence failures still surface as collector failures where testable;
- unchanged server status refreshes persistence without emitting another server event;
- changed status emits one `status.server.updated` event with the expected IDs, status, version, and no credential material;
- production wiring selects the existing SSH probe while tests remain injectable.

### Frontend

Add focused tests that prove:

- mounting the Servers page never calls `/servers/{id}/probe`;
- initial persisted snapshots override stale row status;
- a newer `status.server.updated` snapshot updates only the matching server;
- an older snapshot cannot overwrite a newer SSE state;
- realtime updates preserve selection, search, ordering, active tab, and drawer state;
- manual Probe Host still creates and waits for the task and displays local probing state.

### Verification commands

Run, at minimum:

```text
go test ./internal/collector ./internal/servers ./internal/httpapi
pnpm test:web
pnpm web:build
git diff --check
```

Use the repository's writable local `GOCACHE` through its pnpm tooling or an explicit worktree-local cache if direct Go commands cannot use the system cache.

## Acceptance Criteria

- Re-entering or refreshing the Servers page creates no new `servers.probe` task.
- Refresh List performs only server-list/configuration reads.
- After backend startup, all registered servers receive a live snapshot during the first collector run.
- A server status change appears on an already-open page within approximately 15 to 20 seconds.
- A failing server does not delay healthy-server updates beyond its own five-second probe window.
- Realtime changes do not disturb list or detail interaction state.
- Manual Probe Host remains permission-protected, task-backed, logged, audited, and functional.
- No real SSH or Docker target is changed during automated verification; remote behavior is covered by fake probes.

## Compatibility and Rollout

The change is backward-compatible with the existing server-list API, task API, status-snapshot schema, SSE endpoint, permissions, and configuration. Existing persisted server snapshots are replaced naturally by the first live collector run. Rolling back the binary and frontend restores the previous cached-server collector behavior; no schema rollback is necessary.
