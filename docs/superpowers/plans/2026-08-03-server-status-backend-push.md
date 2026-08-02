# Server Status Backend Push Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Servers-page entry probes with a bounded backend SSH status collector that persists snapshots and pushes changes through the existing global SSE stream.

**Architecture:** Extend `collector.Manager` with an injectable lightweight SSH probe, a five-second per-server timeout, and eight-worker concurrency. Persist live observations in the existing `server` status snapshot scope and publish only changed server snapshots, while the Vue server workbench merges the realtime cache into its existing list without reloading or disturbing interaction state.

**Tech Stack:** Go 1.24, SQLite status snapshots, existing `realtime.Hub` SSE transport, Vue 3 Composition API, Pinia, TypeScript, Vitest, Element Plus.

## Global Constraints

- Use committed baseline `22fc1ae7` or later. That baseline already contains the Docker/app timeout separation in `backend/internal/collector/manager.go` and `manager_test.go`; preserve those fields and tests. Start each implementation task only when `git status --short` has no unexpected new changes.
- Before editing `web/src`, read `design/ant-design-system-portable202606.md` completely as required by `AGENTS.md`; this feature changes behavior only and must not introduce visual or style changes.
- Keep the existing 15-second `AIFAR_COLLECTOR_INTERVAL_SECONDS` schedule, a fixed five-second server-probe timeout, and a fixed maximum of eight concurrent server probes.
- Routine background checks are collector observations, not worker tasks and not audit events. The manual `POST /api/v2/servers/{id}/probe` flow remains task-backed and audited.
- Do not add an API endpoint, database migration, configuration key, user-visible copy, dependency, remote command, inventory query, telemetry query, or Docker operation.
- Do not update canonical `servers.status` from the background collector. The `server` status snapshot is the live-status authority; the canonical row remains configuration/manual-probe state.
- Decrypted passwords and private keys may exist only inside the backend probe call. Never include them in payloads, errors, events, logs, fixtures printed on failure, or frontend state.
- Use fake probes for collector tests. The SSH deadline contract may use an in-process loopback listener that deliberately never completes a handshake; do not connect to or mutate a real SSH/Docker target during automated verification.
- Follow TDD for each task: failing test, observed failure, minimal implementation, passing focused test, then bounded commit.

---

### Task 1: Enforce context deadlines during the SSH handshake

**Files:**
- Create: `backend/internal/adapter/ssh_probe_test.go`
- Modify: `backend/internal/adapter/ssh.go:511-543`

**Interfaces:**
- Consumes: the existing `ProbeSSH(context.Context, store.Server) error` and `dialSSH` TCP connection.
- Produces: the same public interface, with parent cancellation and context deadlines enforced while `ssh.NewClientConn` is waiting for the SSH banner/handshake.
- Keeps: successful SSH clients free of the temporary handshake deadline after connection establishment.

- [ ] **Step 1: Write a failing stalled-handshake regression test**

Create `ssh_probe_test.go` in package `adapter`. Start a loopback listener, accept one TCP connection, hold it open without sending an SSH banner for 500 milliseconds, and call `ProbeSSH` with a 40-millisecond context:

```go
func TestProbeSSHContextDeadlineStopsStalledHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil { return }
		defer conn.Close()
		time.Sleep(500 * time.Millisecond)
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil { t.Fatal(err) }
	port, err := strconv.Atoi(portText)
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = ProbeSSH(ctx, store.Server{
		Host: host, Port: port, Username: "root",
		AuthType: "password", Password: "test-only-password",
	})
	if err == nil { t.Fatal("expected stalled SSH handshake to fail") }
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("context deadline did not stop SSH handshake: %s", elapsed)
	}
}
```

- [ ] **Step 2: Run the adapter test and verify the red state**

Run from `backend/`:

```text
$env:GOCACHE='D:\workspace\aifar-deployment\.cache\go-build'; go test ./internal/adapter -run TestProbeSSHContextDeadlineStopsStalledHandshake -count=1
```

Expected: FAIL after approximately 500 milliseconds because the existing `dialSSH` applies the context only to `DialContext`, not to `ssh.NewClientConn`.

- [ ] **Step 3: Apply and clear a connection deadline around the handshake**

Immediately after the TCP connection succeeds, apply the context deadline and arrange cancellation to interrupt a blocked handshake:

```go
if deadline, ok := ctx.Deadline(); ok {
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, err
	}
}
stopCancel := context.AfterFunc(ctx, func() {
	_ = conn.SetDeadline(time.Now())
})
c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
stopped := stopCancel()
if err != nil {
	_ = conn.Close()
	return nil, err
}
if !stopped && ctx.Err() != nil {
	_ = c.Close()
	_ = conn.Close()
	return nil, ctx.Err()
}
if err := conn.SetDeadline(time.Time{}); err != nil {
	_ = c.Close()
	_ = conn.Close()
	return nil, err
}
return ssh.NewClient(c, chans, reqs), nil
```

Replace the existing `ssh.NewClientConn` block; do not create a second handshake. Clearing the deadline is required so later commands are governed by their existing command contexts rather than the connection-establishment deadline.

- [ ] **Step 4: Run adapter tests**

Run from `backend/`:

```text
$env:GOCACHE='D:\workspace\aifar-deployment\.cache\go-build'; go test ./internal/adapter -count=1
```

Expected: the new deadline regression and all existing adapter tests pass.

- [ ] **Step 5: Commit the SSH deadline contract**

```text
git add backend/internal/adapter/ssh.go backend/internal/adapter/ssh_probe_test.go
git diff --cached --check
git commit -m "fix: honor SSH handshake deadlines"
```

---

### Task 2: Collect live server SSH status in the backend

**Files:**
- Create: `backend/internal/collector/server_status.go`
- Create: `backend/internal/collector/server_status_test.go`
- Modify: `backend/internal/collector/manager.go:27-54`
- Modify: `backend/internal/collector/manager.go:133-164`
- Modify: `backend/internal/collector/manager.go:468-491`

**Interfaces:**
- Consumes: `(*store.Store).ListServers()`, `(*store.Store).GetServer(id, true)`, `(*store.Store).UpsertStatusSnapshot`, `adapter.ProbeSSH`, `logmask.Mask`, and `Publisher.Publish(realtime.Event)`.
- Produces: manager fields `serverProbe func(context.Context, store.Server) error`, `serverProbeTimeout time.Duration`, and `serverProbeWorkers int`; methods `collectLiveServers(context.Context) error`, `collectOneServer(context.Context, store.Server) error`, and `saveSnapshotWithPolicy(context.Context, store.StatusSnapshot, bool) error`.
- Keeps: `collectServers(context.Context) error` as the collector-stage entrypoint and `saveSnapshot(context.Context, store.StatusSnapshot) error` as publish-on-every-collection behavior for all existing non-server scopes.

- [ ] **Step 1: Verify the committed collector baseline before editing**

Run:

```text
git status --short
git show --stat --oneline 22fc1ae7
rg -n "dockerSummaryTimeout|appInstanceTimeout" backend/internal/collector/manager.go backend/internal/collector/manager_test.go
```

Expected: no unexpected worktree changes, baseline `22fc1ae7` is present, and the committed source contains both `dockerSummaryTimeout` and `appInstanceTimeout` plus their regression assertions.

- [ ] **Step 2: Write failing server collector tests**

Create `backend/internal/collector/server_status_test.go` with focused tests using the real temporary SQLite store and an injected probe. The first test must prove decrypted credentials reach only the probe, failure text is masked, the canonical row is unchanged, and only changed snapshots emit events:

```go
package collector

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aifar-deployment/backend/internal/realtime"
	"aifar-deployment/backend/internal/store"
)

func TestServerCollectorProbesWithSecretAndPublishesOnlyChanges(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil { t.Fatal(err) }
	defer db.Close()

	const secret = "collector-password-secret"
	server, err := db.SaveServer(store.Server{
		Name: "node-1", Host: "10.0.0.10", Port: 22,
		Username: "root", AuthType: "password", Password: secret,
		Status: "unknown",
	})
	if err != nil { t.Fatal(err) }

	events := realtime.NewHub()
	ch, unsubscribe := events.Subscribe()
	defer unsubscribe()
	manager := NewManager(db, events, time.Minute)
	manager.serverProbe = func(_ context.Context, target store.Server) error {
		if target.Password != secret { t.Fatalf("probe did not receive decrypted credential") }
		return fmt.Errorf("password=%s connection refused", target.Password)
	}

	if err := manager.collectServers(context.Background()); err != nil { t.Fatal(err) }
	snapshot, err := db.GetStatusSnapshot("server", server.ID)
	if err != nil { t.Fatal(err) }
	if snapshot.Status != "failed" || strings.Contains(snapshot.LastError, secret) {
		t.Fatalf("unsafe failed snapshot: %+v", snapshot)
	}
	first := nextEvent(t, ch)
	if first.Type != "status.server.updated" || first.ResourceID != server.ID {
		t.Fatalf("unexpected server event: %+v", first)
	}
	if strings.Contains(fmt.Sprint(first.Payload), secret) {
		t.Fatalf("credential leaked in event: %+v", first)
	}

	if err := manager.collectServers(context.Background()); err != nil { t.Fatal(err) }
	select {
	case event := <-ch:
		t.Fatalf("unchanged server snapshot was republished: %+v", event)
	case <-time.After(50 * time.Millisecond):
	}
	stored, err := db.GetServer(server.ID, false)
	if err != nil { t.Fatal(err) }
	if stored.Status != "unknown" { t.Fatalf("collector mutated canonical server: %+v", stored) }
}
```

Add a successful-probe test that expects `available` and an empty `lastError`. Add a concurrency/timeout test that creates exactly nine servers, tracks active calls under a mutex, sets `serverProbeWorkers = 2` and `serverProbeTimeout = 25*time.Millisecond`, blocks one fake probe on `<-ctx.Done()`, and proves a fast server still receives an `available` snapshot while maximum concurrency never exceeds two. Add a closed-store test that calls `collectServers` after `db.Close()` and expects a non-nil collector error, separating infrastructure failure from ordinary unreachable-server state.

- [ ] **Step 3: Run the backend test and verify the red state**

Run from `backend/` with a writable cache:

```text
$env:GOCACHE='D:\workspace\aifar-deployment\.cache\go-build'; go test ./internal/collector -run 'TestServerCollector' -count=1
```

Expected: compilation fails because `Manager.serverProbe`, `serverProbeWorkers`, and `serverProbeTimeout` do not exist and `collectServers` still copies cached row state.

- [ ] **Step 4: Add manager defaults and preserve existing timeout fields**

Add these exact fields immediately after `appInstanceTimeout time.Duration` in `Manager`, without changing the committed `dockerSummaryTimeout` or `appInstanceTimeout` behavior:

```go
serverProbe        func(context.Context, store.Server) error
serverProbeTimeout time.Duration
serverProbeWorkers int
```

Add these exact initializers immediately after `appInstanceTimeout: 30 * time.Second` in the `NewManager` struct literal:

```go
serverProbe:        adapter.ProbeSSH,
serverProbeTimeout: 5 * time.Second,
serverProbeWorkers: 8,
```

Replace only the old cached-state body with a delegation:

```go
func (m *Manager) collectServers(ctx context.Context) error {
	return m.collectLiveServers(ctx)
}
```

- [ ] **Step 5: Implement bounded live server collection**

Create `server_status.go`. Use a fixed worker pool rather than one goroutine per server waiting on a semaphore, so parent cancellation stops scheduling new jobs:

```go
package collector

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"aifar-deployment/backend/internal/logmask"
	"aifar-deployment/backend/internal/store"
)

func (m *Manager) collectLiveServers(ctx context.Context) error {
	servers, err := m.store.ListServers()
	if err != nil { return err }
	if len(servers) == 0 { return nil }

	workers := m.serverProbeWorkers
	if workers <= 0 { workers = 1 }
	if workers > len(servers) { workers = len(servers) }
	jobs := make(chan store.Server)
	results := make(chan error, len(servers))
	var wg sync.WaitGroup
	for index := 0; index < workers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for server := range jobs {
				results <- m.collectOneServer(ctx, server)
			}
		}()
	}

	scheduled := 0
schedule:
	for _, server := range servers {
		select {
		case jobs <- server:
			scheduled++
		case <-ctx.Done():
			break schedule
		}
	}
	close(jobs)
	wg.Wait()
	close(results)

	errs := make([]error, 0)
	for index := 0; index < scheduled; index++ {
		if result := <-results; result != nil { errs = append(errs, result) }
	}
	if ctx.Err() != nil { errs = append(errs, ctx.Err()) }
	return errors.Join(errs...)
}

func (m *Manager) collectOneServer(ctx context.Context, public store.Server) error {
	status := "available"
	errText := ""
	server, err := m.store.GetServer(public.ID, true)
	if err == nil {
		timeout := m.serverProbeTimeout
		if timeout <= 0 { timeout = 5 * time.Second }
		child, cancel := context.WithTimeout(ctx, timeout)
		err = m.serverProbe(child, server)
		cancel()
	}
	if err != nil {
		status = "failed"
		errText = logmask.Mask(err.Error())
	}
	payload := map[string]any{
		"id": public.ID, "name": public.Name, "host": public.Host,
		"status": status, "dockerHost": public.DockerHost,
		"updatedAt": public.UpdatedAt,
	}
	return m.saveSnapshotWithPolicy(ctx, store.StatusSnapshot{
		Scope: "server", ResourceID: public.ID, ServerID: public.ID,
		Status: status, LastError: errText, Payload: marshalPayload(payload),
		CollectedAt: time.Now(),
	}, false)
}
```

Before accepting this implementation, ensure a nil injected probe cannot panic: `NewManager` always supplies `adapter.ProbeSSH`, and tests that replace it must supply a non-nil function.

- [ ] **Step 6: Add server-only changed-event publication**

Keep the public behavior of `saveSnapshot` for app/Runtime/Docker scopes and extract its current body:

```go
func (m *Manager) saveSnapshot(ctx context.Context, snapshot store.StatusSnapshot) error {
	return m.saveSnapshotWithPolicy(ctx, snapshot, true)
}

func (m *Manager) saveSnapshotWithPolicy(ctx context.Context, snapshot store.StatusSnapshot, publishUnchanged bool) error {
	if ctx.Err() != nil { return ctx.Err() }
	saved, changed, err := m.store.UpsertStatusSnapshot(snapshot)
	if err != nil { return err }
	if m.events != nil && (changed || publishUnchanged) {
		payload := snapshotEventPayload(saved)
		payload["changed"] = changed
		m.events.Publish(realtime.Event{
			Type: "status." + saved.Scope + ".updated",
			Resource: saved.Scope, ResourceID: saved.ResourceID,
			ServerID: saved.ServerID, InstanceID: instanceIDForSnapshot(saved),
			Status: saved.Status, Version: saved.Version,
			CollectedAt: saved.CollectedAt, Payload: payload,
		})
	}
	return nil
}
```

The existing `TestSaveSnapshotPublishesEveryCollection` must remain green, proving non-server behavior was not changed.

- [ ] **Step 7: Run focused and package backend tests**

Run from `backend/`:

```text
$env:GOCACHE='D:\workspace\aifar-deployment\.cache\go-build'; go test ./internal/collector -run 'TestServerCollector|TestSaveSnapshotPublishesEveryCollection|TestCollectorUsesSeparateRemoteCheckTimeoutBudgets' -count=1
$env:GOCACHE='D:\workspace\aifar-deployment\.cache\go-build'; go test ./internal/collector ./internal/servers ./internal/httpapi -count=1
```

Expected: all selected packages pass; no real SSH connection is attempted because the collector tests inject a fake probe.

- [ ] **Step 8: Stage only this feature and commit**

Stage the two new files and the manager integration from the clean committed baseline:

```text
git add backend/internal/collector/manager.go backend/internal/collector/server_status.go backend/internal/collector/server_status_test.go
git diff --cached --check
git diff --cached -- backend/internal/collector/manager.go backend/internal/collector/server_status.go backend/internal/collector/server_status_test.go
git commit -m "feat: collect live server status"
```

Expected staged content: only server probe fields/defaults, cached-body delegation, server collection code, server tests, and snapshot publish-policy extraction. `backend/internal/collector/manager_test.go` remains unchanged because its timeout-separation test is already committed in the baseline.

---

### Task 3: Add typed server snapshot merging to the frontend

**Files:**
- Create: `web/src/servers/realtimeStatus.ts`
- Create: `web/src/servers/realtimeStatus.test.ts`
- Modify: `web/src/stores/realtime.ts:59-74`
- Modify: `web/src/stores/realtime.test.ts:237-277`

**Interfaces:**
- Consumes: `StatusSnapshot`, existing freshness/version logic in `useRealtimeStore`, and `ServerRecord.updatedAt`.
- Produces: Pinia getter `serverSnapshot(serverId: string): StatusSnapshot | undefined` and pure function `applyRealtimeStatusToServer(server: ServerRecord, snapshot?: StatusSnapshot): ServerRecord`.
- Freshness rule: a server snapshot with an invalid scope/resource ID or a `collectedAt` older than a valid canonical `server.updatedAt` is ignored.

- [ ] **Step 1: Write failing realtime getter and merge tests**

Add a server-envelope case to `realtime.test.ts`:

```ts
it('indexes a server status event by server id', () => {
  const store = useRealtimeStore()
  store.applyEvent({
    type: 'status.server.updated',
    resource: 'server',
    resourceId: 'server-9',
    serverId: 'server-9',
    status: 'failed',
    version: 4,
    collectedAt: '2026-08-03T10:00:00Z',
    payload: {
      scope: 'server', resourceId: 'server-9', serverId: 'server-9',
      status: 'failed', lastError: 'connection refused', version: 4,
      collectedAt: '2026-08-03T10:00:00Z', updatedAt: '2026-08-03T10:00:01Z',
      payload: { status: 'failed' }
    }
  })
  expect(store.serverSnapshot('server-9')?.status).toBe('failed')
})
```

Create `realtimeStatus.test.ts` with tests that a valid newer snapshot replaces only `status` and `lastError`, wrong scope/ID returns the original object, and a snapshot collected before `server.updatedAt` is ignored.

- [ ] **Step 2: Run focused frontend tests and verify the red state**

Run from `web/`:

```text
node node_modules/vitest/vitest.mjs run src/stores/realtime.test.ts src/servers/realtimeStatus.test.ts
```

Expected: failure because `serverSnapshot` and `applyRealtimeStatusToServer` do not exist.

- [ ] **Step 3: Add the realtime-store server getter**

Add beside the existing Docker getter:

```ts
serverSnapshot(state): (serverId: string) => StatusSnapshot | undefined {
  return (serverId: string) => state.statusSnapshotsByKey[snapshotKey('server', serverId)]
},
```

Do not change `mergeStatusSnapshots`, `compareSnapshotFreshness`, or event parsing; the existing generic envelope already supports `status.server.updated`.

- [ ] **Step 4: Implement the pure server merge helper**

Create `realtimeStatus.ts`:

```ts
import type { StatusSnapshot } from '../stores/realtime'
import type { ServerRecord } from './types'

export function applyRealtimeStatusToServer(server: ServerRecord, snapshot?: StatusSnapshot): ServerRecord {
  if (!snapshot || snapshot.scope !== 'server' || snapshot.resourceId !== server.id) return server
  const observedAt = Date.parse(snapshot.collectedAt ?? '')
  const configuredAt = Date.parse(server.updatedAt ?? '')
  if (Number.isFinite(observedAt) && Number.isFinite(configuredAt) && observedAt < configuredAt) return server

  const status = String(snapshot.status ?? '').trim()
  if (!status) return server
  const lastError = String(snapshot.lastError ?? '').trim()
  if (server.status === status && String(server.lastError ?? '') === lastError) return server
  return { ...server, status, lastError }
}
```

The helper must not copy arbitrary snapshot payload fields into a server record.

- [ ] **Step 5: Run focused tests and build type checking**

Run from `web/`:

```text
node node_modules/vitest/vitest.mjs run src/stores/realtime.test.ts src/servers/realtimeStatus.test.ts
pnpm run build
```

Expected: both test files pass and `vue-tsc --noEmit` succeeds.

- [ ] **Step 6: Commit the typed realtime boundary**

```text
git add web/src/stores/realtime.ts web/src/stores/realtime.test.ts web/src/servers/realtimeStatus.ts web/src/servers/realtimeStatus.test.ts
git diff --cached --check
git commit -m "feat: merge realtime server snapshots"
```

---

### Task 4: Make the Servers page read-only on entry and react to snapshots

**Files:**
- Create: `web/src/servers/useServerWorkbench.test.ts`
- Create: `web/src/views/ServersView.test.ts`
- Modify: `web/src/servers/useServerWorkbench.ts:32-64`
- Modify: `web/src/servers/useServerWorkbench.ts:125-194`
- Modify: `web/src/views/ServersView.vue:46-95`

**Interfaces:**
- Consumes: `serverSnapshot(serverId)` from Task 3 and `applyRealtimeStatusToServer` from Task 3.
- Produces: optional `ServerSnapshotResolver = (serverId: string) => StatusSnapshot | undefined`, workbench method `applyStatusSnapshots(): void`, and a Servers page that watches `realtime.statusRevision` without calling a probe API on mount.
- Keeps: `probe(row)`, permission enforcement, task waiting, local `probingIds`, list selection, search, ordering, drawer, and active tab behavior.

- [ ] **Step 1: Read the required design-system file before editing Vue code**

Run:

```text
Get-Content -LiteralPath 'design/ant-design-system-portable202606.md' -Raw
```

Expected: the full file is read. No style change is needed because this task only removes an automatic side effect and adds state synchronization.

- [ ] **Step 2: Write failing workbench synchronization tests**

Mock `./api` in `useServerWorkbench.test.ts`. Cover initial list merge and preservation of interaction state:

```ts
it('applies live snapshots without resetting workbench state', async () => {
  const snapshots = new Map<string, StatusSnapshot>()
  listServersMock.mockResolvedValueOnce([
    { id: 'srv-1', name: 'one', host: '10.0.0.1', port: 22, username: 'root', authType: 'password', status: 'unknown' }
  ])
  snapshots.set('srv-1', {
    scope: 'server', resourceId: 'srv-1', status: 'available', lastError: '',
    version: 1, collectedAt: '2026-08-03T10:00:00Z'
  })
  const workbench = useServerWorkbench((key) => key, (id) => snapshots.get(id))
  await workbench.load()
  workbench.selectedId.value = 'srv-1'
  workbench.search.value = 'one'
  workbench.drawer.value = true
  workbench.activeTab.value = 'access'

  snapshots.set('srv-1', {
    scope: 'server', resourceId: 'srv-1', status: 'failed', lastError: 'timeout',
    version: 2, collectedAt: '2026-08-03T10:00:15Z'
  })
  workbench.applyStatusSnapshots()

  expect(workbench.servers.value[0]).toMatchObject({ status: 'failed', lastError: 'timeout' })
  expect(workbench.selectedId.value).toBe('srv-1')
  expect(workbench.search.value).toBe('one')
  expect(workbench.drawer.value).toBe(true)
  expect(workbench.activeTab.value).toBe('access')
})
```

Add a manual-probe test with a deferred `waitTaskDone` promise. Assert the row becomes `probing`, `applyStatusSnapshots()` does not overwrite it while its ID is in `probingIds`, and after task completion `listServers` is called again.

- [ ] **Step 3: Write a failing page-mount regression test**

Create `ServersView.test.ts` using `shallowMount`, an active Pinia instance, mocked i18n/permissions, and mocked server API functions. Mount the page and flush promises:

```ts
it('loads the server list without starting probe tasks', async () => {
  listServersMock.mockResolvedValue([
    { id: 'srv-1', name: 'one', host: '10.0.0.1', port: 22, username: 'root', authType: 'password', status: 'unknown' }
  ])
  getServerDefaultsMock.mockResolvedValueOnce({ defaultDeployDir: '/aifar/apps' })
  probeServerMock.mockResolvedValueOnce({ taskId: 'tsk-probe-1' })
  waitTaskDoneMock.mockResolvedValueOnce('success')
  shallowMount(ServersView, { global: { plugins: [createPinia()] } })
  await flushPromises()
  expect(listServersMock).toHaveBeenCalled()
  expect(probeServerMock).not.toHaveBeenCalled()
})
```

Expected red state: the current `onMounted` calls `probeAllOnce()` for manageable users, so the one-server fixture calls `probeServerMock` once.

- [ ] **Step 4: Run the two focused tests and verify failures**

Run from `web/`:

```text
node node_modules/vitest/vitest.mjs run src/servers/useServerWorkbench.test.ts src/views/ServersView.test.ts
```

Expected: missing resolver/apply method failures and a `probeServerMock` call during mount.

- [ ] **Step 5: Inject the snapshot resolver into the workbench**

Update `useServerWorkbench.ts`:

```ts
import type { StatusSnapshot } from '../stores/realtime'
import { applyRealtimeStatusToServer } from './realtimeStatus'

export type ServerSnapshotResolver = (serverId: string) => StatusSnapshot | undefined

export function useServerWorkbench(
  t: (key: string, params?: Record<string, unknown>) => string,
  resolveSnapshot: ServerSnapshotResolver = () => undefined
) {
  function mergeLiveStatus(server: ServerRecord) {
    return probingIds.value.has(server.id)
      ? server
      : applyRealtimeStatusToServer(server, resolveSnapshot(server.id))
  }

  async function load() {
    servers.value = (await listServers()).map(mergeLiveStatus)
    if (!selectedId.value && servers.value.length) selectedId.value = servers.value[0].id
    if (selectedId.value && !servers.value.some((server) => server.id === selectedId.value)) {
      selectedId.value = servers.value[0]?.id ?? ''
    }
  }

  function applyStatusSnapshots() {
    servers.value = servers.value.map(mergeLiveStatus)
  }
}
```

Delete `defaultProbeDone`, `probeSelectedOnce`, and `probeAllOnce`. Return `applyStatusSnapshots` alongside the existing public workbench members. Leave `probe(row)` unchanged except for existing formatting required by TypeScript.

- [ ] **Step 6: Wire the Servers page to the global realtime store**

Update the script section only:

```ts
import { computed, onMounted, watch } from 'vue'
import { useRealtimeStore } from '../stores/realtime'

const realtime = useRealtimeStore()
const {
  filteredServers,
  selectedServer,
  selectedId,
  search,
  drawer,
  activeTab,
  probingIds,
  form,
  summary,
  loadDefaults,
  load,
  open,
  save,
  remove,
  reorder,
  probe,
  applyStatusSnapshots
} = useServerWorkbench(t, (serverId) => realtime.serverSnapshot(serverId))

watch(() => realtime.statusRevision, () => {
  applyStatusSnapshots()
})

onMounted(async () => {
  await loadDefaults()
  await load()
})
```

Remove `probeAllOnce` from destructuring and remove the permission-gated mount probe. Do not change the template, styles, Refresh List handler, or manual `probeServerHost` handler.

- [ ] **Step 7: Run focused and full frontend verification**

Run from `web/` first:

```text
node node_modules/vitest/vitest.mjs run src/stores/realtime.test.ts src/servers/realtimeStatus.test.ts src/servers/useServerWorkbench.test.ts src/views/ServersView.test.ts
pnpm run build
```

Then run from the repository root:

```text
pnpm test:web
pnpm web:build
```

Expected: all tests pass, the production bundle builds, and there is no API call to `/servers/{id}/probe` during page mount.

- [ ] **Step 8: Commit the page behavior change**

```text
git add web/src/servers/useServerWorkbench.ts web/src/servers/useServerWorkbench.test.ts web/src/views/ServersView.vue web/src/views/ServersView.test.ts
git diff --cached --check
git commit -m "fix: stop probing servers on page entry"
```

---

### Task 5: Run integrated acceptance and preserve repository history

**Files:**
- Verify: all files changed in Tasks 1-3
- Update without staging unrelated history: `memory.md`

**Interfaces:**
- Consumes: backend `status.server.updated`, Pinia `serverSnapshot`, and the Servers page synchronization from prior tasks.
- Produces: verified backend/frontend behavior, an auditable memory entry, and a clean feature-only commit set while unrelated worktree changes remain preserved.

- [ ] **Step 1: Run the complete relevant backend gate**

Run from the repository root:

```text
$env:GOCACHE='D:\workspace\aifar-deployment\.cache\go-build'; pnpm test
```

Expected: `go test ./...` passes. If an unrelated dirty-worktree test fails, record the exact package/test and verify the focused collector/server/httpapi packages still pass; do not change unrelated code to force this feature through.

- [ ] **Step 2: Run the complete frontend gate**

```text
pnpm test:web
pnpm web:build
```

Expected: all Vitest files pass and the Vue/TypeScript production build succeeds.

- [ ] **Step 3: Check formatting and diff scope**

```text
git diff --check
git status --short
git log -4 --oneline
```

Expected: no whitespace errors in feature files; four feature commits follow the plan commit. The MySQL, timeout-separation, and earlier documentation work remain part of committed baseline `22fc1ae7`; only the required end-of-task `memory.md` entry may remain unstaged.

- [ ] **Step 4: Perform read-only page acceptance**

Using an already-running local panel with an authenticated session, record the newest `servers.probe` task ID/time, navigate away from and back to `/servers` twice, and wait for the list to render. Verify:

```text
No new servers.probe task is created by either page entry.
Refresh List reloads data without creating a probe task.
The global realtime indicator remains connected.
Selection, search text, and active detail tab survive a pushed status update.
```

Do not stop, restart, edit, or probe a real target as part of this acceptance. If no authenticated local panel is available, report browser acceptance as not run and rely on the component/API-call regression test rather than fabricating evidence.

- [ ] **Step 5: Append the reusable outcome to repository memory**

Append a concise entry under `## 2026-08-03` in `memory.md` stating that page entry no longer creates probe tasks, the collector uses 15-second scheduling with five-second/eight-worker SSH checks, server changes arrive through `status.server.updated`, and list interaction state is preserved. Do not include credentials, addresses, tokens, or long logs.

Leave the new `memory.md` entry unstaged unless the user explicitly asks to include repository memory in a feature commit.

- [ ] **Step 6: Verify final commits contain only feature files**

```text
git show --stat --oneline HEAD
git show --stat --oneline HEAD~1
git show --stat --oneline HEAD~2
git show --stat --oneline HEAD~3
```

Expected commit subjects:

```text
fix: stop probing servers on page entry
feat: merge realtime server snapshots
feat: collect live server status
fix: honor SSH handshake deadlines
```

If any new unrelated worktree change appears during execution, stop before staging the affected file and report the exact overlap. Never commit or discard someone else's changes to satisfy the expected history.
