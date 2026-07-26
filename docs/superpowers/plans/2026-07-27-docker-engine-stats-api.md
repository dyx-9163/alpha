# Docker Engine Stats API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make AIFAR Runtime Pod metric refresh use the remote Docker Engine HTTP API for `tcp/http/https` hosts so the panel machine does not need a Docker CLI.

**Architecture:** Add a focused Docker stats decoder and single-container HTTP reader to the existing lightweight Adapter, then add a four-worker batch collector that preserves successful rows when peers fail. Route `DockerContainerStatsForServer` to this collector for Engine API hosts and change the Runtime response builder to map partial results before appending warnings; SSH collection, frontend contracts, and `aifar-agent` remain unchanged.

**Tech Stack:** Go 1.24, standard library `net/http`, `httptest`, `errors`, `sync`, existing AIFAR Adapter and Chi HTTP API tests.

## Global Constraints

- Use `GET /containers/{id}/stats?stream=false` for `tcp://`, `http://`, and `https://` Docker Hosts.
- Maximum concurrent stats requests is exactly 4; do not add a configuration option.
- Preserve all successful container stats when one or more sibling requests fail.
- Stats failures add a warning but do not change Runtime, Deployment, Service, or Pod health status.
- Do not fall back to the panel machine's Docker CLI for Engine API hosts.
- Preserve the existing SSH remote `docker stats` path.
- Do not change `web/src`, `backend/cmd/aifar-agent`, database schema, task types, or audit types.
- Do not add the Docker SDK or another dependency.
- Follow TDD: observe each targeted test fail for the intended reason before implementation.
- Execute implementation in an isolated worktree created with `superpowers:using-git-worktrees`.

## File Map

- Create `backend/internal/adapter/docker_stats_test.go`: Engine API decoding, calculation, concurrency, partial-failure, and routing tests.
- Modify `backend/internal/adapter/docker_api.go`: stats payload types, calculations, HTTP reader, and batch collector.
- Modify `backend/internal/adapter/docker.go`: Engine API host routing while preserving SSH behavior.
- Modify `backend/internal/httpapi/containers_aifar_runtime.go`: retain successful stats when the collector also returns an error.
- Modify `backend/internal/httpapi/containers_aifar_runtime_test.go`: Runtime response regression coverage.

---

### Task 1: Decode and calculate one Docker Engine stats response

**Files:**
- Create: `backend/internal/adapter/docker_stats_test.go`
- Modify: `backend/internal/adapter/docker_api.go`

**Interfaces:**
- Consumes: existing `dockerAPIJSON(ctx, method, host, apiPath, query, out)` and `DockerContainerStat`.
- Produces: `dockerAPIContainerStats(ctx context.Context, host, id string) (DockerContainerStat, error)`.
- Produces: `dockerAPIStatsCPUPercent(payload dockerAPIContainerStatsPayload) float64`.
- Produces: `dockerAPIStatsMemory(payload dockerAPIContainerStatsPayload) (usage uint64, limit uint64, percent float64)`.

- [ ] **Step 1: Write the failing request and metric test**

Create `backend/internal/adapter/docker_stats_test.go` using a real `httptest.Server`:

```go
package adapter

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDockerAPIContainerStatsCalculatesCPUAndCgroupV1Memory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/containers/aifar-pod%2Fpermission/stats" {
			t.Fatalf("path = %q", r.URL.EscapedPath())
		}
		if got := r.URL.Query().Get("stream"); got != "false" {
			t.Fatalf("stream = %q, want false", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"container-1","name":"/aifar-pod-permission",
			"cpu_stats":{"cpu_usage":{"total_usage":1200,"percpu_usage":[600,600]},"system_cpu_usage":5000,"online_cpus":2},
			"precpu_stats":{"cpu_usage":{"total_usage":1000},"system_cpu_usage":4000},
			"memory_stats":{"usage":1000,"limit":2048,"stats":{"total_inactive_file":200,"inactive_file":50}}
		}`))
	}))
	defer server.Close()

	stat, err := dockerAPIContainerStats(context.Background(), server.URL, "aifar-pod/permission")
	if err != nil {
		t.Fatal(err)
	}
	if stat.ID != "container-1" || stat.Name != "aifar-pod-permission" {
		t.Fatalf("identity = %+v", stat)
	}
	if math.Abs(stat.CPUPerc-40) > 0.001 || math.Abs(stat.MemPerc-39.0625) > 0.001 {
		t.Fatalf("percentages = cpu %.4f memory %.4f", stat.CPUPerc, stat.MemPerc)
	}
	if stat.MemUsage != "800 B / 2.0 KiB" || stat.RawCPUPerc != "40.00%" || stat.RawMemPercent != "39.06%" {
		t.Fatalf("formatted stats = %+v", stat)
	}
}
```

- [ ] **Step 2: Add failing edge-case calculation tests**

Define a test-only `statsPayload` constructor that only fills fields. Add table cases for:

```go
tests := []struct {
	name       string
	payload    dockerAPIContainerStatsPayload
	wantCPU    float64
	wantUsage uint64
	wantLimit uint64
	wantMem    float64
}{
	{
		name: "online cpu fallback and cgroup v2 cache",
		payload: statsPayload(1200, 1000, 5000, 4000, 0, []uint64{1, 1}, 1000, 2000, map[string]uint64{"inactive_file": 250}),
		wantCPU: 40, wantUsage: 750, wantLimit: 2000, wantMem: 37.5,
	},
	{
		name: "counter rollback and zero limit",
		payload: statsPayload(900, 1000, 3000, 4000, 2, nil, 100, 0, nil),
		wantCPU: 0, wantUsage: 100, wantLimit: 0, wantMem: 0,
	},
	{
		name: "cache larger than usage",
		payload: statsPayload(0, 0, 0, 0, 0, nil, 100, 1000, map[string]uint64{"total_inactive_file": 200}),
		wantCPU: 0, wantUsage: 0, wantLimit: 1000, wantMem: 0,
	},
}
```

For each case, call `dockerAPIStatsCPUPercent` and `dockerAPIStatsMemory`; compare floats with a `0.001` tolerance. Do not reproduce production formulas inside the test helper.

Use this constructor so every input field is explicit:

```go
func statsPayload(currentCPU, previousCPU, currentSystem, previousSystem, onlineCPUs uint64, perCPU []uint64, usage, limit uint64, memory map[string]uint64) dockerAPIContainerStatsPayload {
	var payload dockerAPIContainerStatsPayload
	payload.CPUStats.CPUUsage.TotalUsage = currentCPU
	payload.CPUStats.CPUUsage.PercpuUsage = perCPU
	payload.CPUStats.SystemCPUUsage = currentSystem
	payload.CPUStats.OnlineCPUs = onlineCPUs
	payload.PreCPUStats.CPUUsage.TotalUsage = previousCPU
	payload.PreCPUStats.SystemCPUUsage = previousSystem
	payload.MemoryStats.Usage = usage
	payload.MemoryStats.Limit = limit
	payload.MemoryStats.Stats = memory
	return payload
}
```

- [ ] **Step 3: Run the targeted test and verify RED**

Run from `backend/`:

```powershell
go test ./internal/adapter -run 'TestDockerAPI(ContainerStats|StatsCalculation)' -count=1
```

Expected: compilation fails because the payload type and stats functions do not exist.

- [ ] **Step 4: Add payload types and guarded calculations**

In `backend/internal/adapter/docker_api.go`, add:

```go
type dockerAPIContainerStatsPayload struct {
	ID          string                       `json:"id"`
	Name        string                       `json:"name"`
	CPUStats    dockerAPIStatsCPU             `json:"cpu_stats"`
	PreCPUStats dockerAPIStatsCPU             `json:"precpu_stats"`
	MemoryStats dockerAPIStatsMemoryCounters  `json:"memory_stats"`
}

type dockerAPIStatsCPU struct {
	CPUUsage struct {
		TotalUsage  uint64   `json:"total_usage"`
		PercpuUsage []uint64 `json:"percpu_usage"`
	} `json:"cpu_usage"`
	SystemCPUUsage uint64 `json:"system_cpu_usage"`
	OnlineCPUs     uint64 `json:"online_cpus"`
}

type dockerAPIStatsMemoryCounters struct {
	Usage uint64            `json:"usage"`
	Limit uint64            `json:"limit"`
	Stats map[string]uint64 `json:"stats"`
}
```

Implement CPU calculation with guarded unsigned subtraction. Use `OnlineCPUs`, falling back to `len(PercpuUsage)`. Return 0 when either counter does not advance or CPU count is 0.

```go
func dockerAPIStatsCPUPercent(payload dockerAPIContainerStatsPayload) float64 {
	current, previous := payload.CPUStats, payload.PreCPUStats
	if current.CPUUsage.TotalUsage <= previous.CPUUsage.TotalUsage || current.SystemCPUUsage <= previous.SystemCPUUsage {
		return 0
	}
	cpuCount := current.OnlineCPUs
	if cpuCount == 0 {
		cpuCount = uint64(len(current.CPUUsage.PercpuUsage))
	}
	if cpuCount == 0 {
		return 0
	}
	return float64(current.CPUUsage.TotalUsage-previous.CPUUsage.TotalUsage) /
		float64(current.SystemCPUUsage-previous.SystemCPUUsage) * float64(cpuCount) * 100
}
```

Implement memory calculation with a presence check for `total_inactive_file`, then fallback to `inactive_file`; clamp cache subtraction at zero and return a zero percentage when limit is 0:

```go
func dockerAPIStatsMemory(payload dockerAPIContainerStatsPayload) (uint64, uint64, float64) {
	usage, limit := payload.MemoryStats.Usage, payload.MemoryStats.Limit
	cache, ok := payload.MemoryStats.Stats["total_inactive_file"]
	if !ok {
		cache = payload.MemoryStats.Stats["inactive_file"]
	}
	if cache >= usage {
		usage = 0
	} else {
		usage -= cache
	}
	if limit == 0 {
		return usage, limit, 0
	}
	return usage, limit, float64(usage) / float64(limit) * 100
}
```

- [ ] **Step 5: Implement the single-container HTTP reader**

Use `url.PathEscape`, `stream=false`, existing `formatBytes`, and normalized names:

```go
func dockerAPIContainerStats(ctx context.Context, host, id string) (DockerContainerStat, error) {
	var payload dockerAPIContainerStatsPayload
	query := url.Values{"stream": []string{"false"}}
	path := "/containers/" + url.PathEscape(id) + "/stats"
	if err := dockerAPIJSON(ctx, http.MethodGet, host, path, query, &payload); err != nil {
		return DockerContainerStat{}, err
	}
	cpu := dockerAPIStatsCPUPercent(payload)
	usage, limit, memory := dockerAPIStatsMemory(payload)
	name := strings.TrimPrefix(strings.TrimSpace(payload.Name), "/")
	if name == "" {
		name = id
	}
	return DockerContainerStat{
		ID: firstNonEmptyString(payload.ID, id), Name: name,
		CPUPerc: cpu, MemPerc: memory,
		MemUsage: formatBytes(int64(usage)) + " / " + formatBytes(int64(limit)),
		RawCPUPerc: fmt.Sprintf("%.2f%%", cpu),
		RawMemPercent: fmt.Sprintf("%.2f%%", memory),
	}, nil
}
```

- [ ] **Step 6: Run Task 1 tests and commit**

```powershell
go test ./internal/adapter -run 'TestDockerAPI(ContainerStats|StatsCalculation)' -count=1
git add backend/internal/adapter/docker_api.go backend/internal/adapter/docker_stats_test.go
git commit -m "feat(adapter): decode Docker Engine stats"
```

Expected: tests pass; commit contains only the two listed files.

---

### Task 2: Add bounded batch collection and remove the local CLI dependency

**Files:**
- Modify: `backend/internal/adapter/docker_api.go`
- Modify: `backend/internal/adapter/docker.go:556-582`
- Modify: `backend/internal/adapter/docker_stats_test.go`

**Interfaces:**
- Consumes: Task 1 `dockerAPIContainerStats`.
- Produces: `dockerAPIContainerStatsBatch(ctx context.Context, host string, ids []string) ([]DockerContainerStat, error)`.
- Changes: `DockerContainerStatsForServer` may return successful stats together with a non-nil joined error.

- [ ] **Step 1: Write the failing no-local-CLI routing test**

Add imports `sync/atomic` and `aifar-deployment/backend/internal/store`. Force an empty executable path and require a successful HTTP request:

```go
func TestDockerContainerStatsForServerUsesEngineAPIWithoutLocalCLI(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{
			"id":"container-1","name":"/pod-1",
			"cpu_stats":{"cpu_usage":{"total_usage":1200},"system_cpu_usage":5000,"online_cpus":1},
			"precpu_stats":{"cpu_usage":{"total_usage":1000},"system_cpu_usage":4000},
			"memory_stats":{"usage":500,"limit":1000,"stats":{}}
		}`))
	}))
	defer server.Close()

	stats, err := DockerContainerStatsForServer(context.Background(), store.Server{DockerHost: server.URL}, []string{"pod-1"})
	if err != nil || len(stats) != 1 || calls.Load() != 1 {
		t.Fatalf("stats=%+v calls=%d err=%v", stats, calls.Load(), err)
	}
}
```

- [ ] **Step 2: Write failing partial-failure and concurrency tests**

Add `TestDockerAPIContainerStatsBatchKeepsSuccessfulRows`. Pass `[]string{"", "good", "missing", "good"}`; return valid JSON for `good` and 404 for `missing`. Require exactly one stat plus a non-nil error containing `missing`.

```go
func TestDockerAPIContainerStatsBatchKeepsSuccessfulRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/missing/") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{
			"id":"good-id","name":"/good",
			"cpu_stats":{"cpu_usage":{"total_usage":2},"system_cpu_usage":2,"online_cpus":1},
			"precpu_stats":{"cpu_usage":{"total_usage":1},"system_cpu_usage":1},
			"memory_stats":{"usage":1,"limit":2,"stats":{}}
		}`))
	}))
	defer server.Close()

	stats, err := dockerAPIContainerStatsBatch(context.Background(), server.URL, []string{"", "good", "missing", "good"})
	if len(stats) != 1 || stats[0].Name != "good" {
		t.Fatalf("partial stats = %+v", stats)
	}
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("partial error = %v", err)
	}
}
```

Add `TestDockerAPIContainerStatsBatchLimitsConcurrencyToFour`. The test server must:

- increment an atomic active count;
- send to a buffered `entered` channel;
- block on a shared `release` channel;
- decrement active after release.

Run a five-ID batch in a goroutine. Receive four `entered` events with one-second timeouts, assert the fifth does not arrive during 50 milliseconds, close `release`, then require five successful stats. This proves the exact limit without relying on timing-based maximum counters.

```go
func TestDockerAPIContainerStatsBatchLimitsConcurrencyToFour(t *testing.T) {
	entered := make(chan struct{}, 5)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		id := strings.Split(strings.Trim(r.URL.Path, "/"), "/")[1]
		_, _ = fmt.Fprintf(w, `{"id":%q,"name":%q,"memory_stats":{"usage":1,"limit":2,"stats":{}}}`, id, "/"+id)
	}))
	defer server.Close()

	type batchResult struct { stats []DockerContainerStat; err error }
	done := make(chan batchResult, 1)
	go func() {
		stats, err := dockerAPIContainerStatsBatch(context.Background(), server.URL, []string{"p1", "p2", "p3", "p4", "p5"})
		done <- batchResult{stats: stats, err: err}
	}()
	for index := 0; index < 4; index++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("four workers did not enter")
		}
	}
	select {
	case <-entered:
		t.Fatal("fifth request started before a worker was released")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	result := <-done
	if result.err != nil || len(result.stats) != 5 {
		t.Fatalf("stats=%+v err=%v", result.stats, result.err)
	}
}
```

Add `fmt`, `strings`, and `time` to the test imports.

- [ ] **Step 3: Run Task 2 tests and verify RED**

```powershell
go test ./internal/adapter -run 'TestDocker(ContainerStatsForServer|APIContainerStatsBatch)' -count=1
```

Expected: the batch function is undefined and the server-aware function tries to execute a missing local Docker CLI.

- [ ] **Step 4: Implement the deterministic four-worker collector**

Add `errors` and `sync` imports, `const dockerStatsMaxConcurrency = 4`, normalize IDs, and preserve input ordering:

```go
func dockerAPIContainerStatsBatch(ctx context.Context, host string, ids []string) ([]DockerContainerStat, error) {
	ids = normalizeDockerArgs(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	type result struct { stat DockerContainerStat; err error }
	results := make([]result, len(ids))
	jobs := make(chan int)
	workers := min(dockerStatsMaxConcurrency, len(ids))
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				stat, err := dockerAPIContainerStats(ctx, host, ids[index])
				if err != nil { err = fmt.Errorf("container %s: %w", ids[index], err) }
				results[index] = result{stat: stat, err: err}
			}
		}()
	}
	for index := range ids { jobs <- index }
	close(jobs)
	wg.Wait()
	stats := make([]DockerContainerStat, 0, len(ids))
	errs := make([]error, 0)
	for _, item := range results {
		if item.err != nil { errs = append(errs, item.err); continue }
		stats = append(stats, item.stat)
	}
	return stats, errors.Join(errs...)
}
```

Every request already carries `ctx`; do not add another timeout or configuration knob.

- [ ] **Step 5: Route Engine API hosts to the collector**

In `DockerContainerStatsForServer`, replace only the API-host branch:

```go
if dockerAPIHost(server.DockerHost) {
	return dockerAPIContainerStatsBatch(ctx, server.DockerHost, ids)
}
```

Leave the `dockerSSHOutput` branch semantically unchanged. Do not change `DockerContainerStats`, which remains the explicit local/CLI helper.

- [ ] **Step 6: Run Task 2 tests and commit**

```powershell
go test ./internal/adapter -run 'TestDocker(ContainerStatsForServer|APIContainerStatsBatch|APIContainerStats)' -count=1
git add backend/internal/adapter/docker.go backend/internal/adapter/docker_api.go backend/internal/adapter/docker_stats_test.go
git commit -m "fix(adapter): collect remote Docker stats over API"
```

Expected: routing, partial-result, duplicate-input, and four-worker tests pass.

---

### Task 3: Preserve partial stats in the Runtime response

**Files:**
- Modify: `backend/internal/httpapi/containers_aifar_runtime.go:1299-1308`
- Modify: `backend/internal/httpapi/containers_aifar_runtime_test.go`

**Interfaces:**
- Consumes: partial stats plus non-nil error from `DockerContainerStatsForServer`.
- Produces: unchanged `aifarRuntimeResponse` with successful Pod metrics and one warning for failed peers.

- [ ] **Step 1: Write the failing Runtime regression test**

Add `TestAIFARRuntimeStatsKeepsPartialMetricsWithoutDegradingRuntime`:

1. Create API/store/token using `newAuthzTestAPI`.
2. Set `api.aifarAgentStatus` to return `aifarRuntimeAgent{Status: "running"}`.
3. Use `seedAIFARRuntimeFixture` and add a second stored Pod with `PodID: "r2"`, container name `aifar-pod-admin-permission-rev-1-r2`, status `running`, and `Ready: true`.
4. Serve `/_ping`, `/containers/json`, one successful stats endpoint, and one 404 stats endpoint.

Use the returned instance ID when saving the second Pod:

```go
if _, err := db.SaveAIFARPod(store.AIFARPod{
	InstanceID: instance.ID, ServiceName: "permission", Revision: "rev-1",
	PodID: "r2", ContainerName: "aifar-pod-admin-permission-rev-1-r2",
	Port: 38010, Status: "running", Ready: true,
}); err != nil {
	t.Fatal(err)
}
```

The container list and successful stats response must be:

```go
case "/containers/json":
	_ = json.NewEncoder(w).Encode([]map[string]any{
		{"Id": "good-id", "Names": []string{"/aifar-pod-admin-permission-rev-1-r1"}, "Image": "aifar-permission:rev-1", "State": "running", "Status": "Up 1 minute (healthy)"},
		{"Id": "missing-id", "Names": []string{"/aifar-pod-admin-permission-rev-1-r2"}, "Image": "aifar-permission:rev-1", "State": "running", "Status": "Up 1 minute (healthy)"},
	})
case "/containers/aifar-pod-admin-permission-rev-1-r1/stats":
	_, _ = w.Write([]byte(`{
		"id":"good-id","name":"/aifar-pod-admin-permission-rev-1-r1",
		"cpu_stats":{"cpu_usage":{"total_usage":1200},"system_cpu_usage":5000,"online_cpus":1},
		"precpu_stats":{"cpu_usage":{"total_usage":1000},"system_cpu_usage":4000},
		"memory_stats":{"usage":500,"limit":1000,"stats":{}}
	}`))
case "/containers/aifar-pod-admin-permission-rev-1-r2/stats":
	http.NotFound(w, r)
```

Request `/api/v2/containers/aifar/runtime?serverId=<id>&includePods=1&includeStats=1`. Assert HTTP 200, two Pods, `RuntimeStatus == "ready"`, positive metrics for r1, zero/empty metrics for r2, and a warning containing both `failed to read Docker stats` and `r2`.

Locate the response Pods without assuming response order:

```go
pods := map[string]aifarRuntimePod{}
for _, pod := range body.Pods {
	pods[pod.PodID] = pod
}
good, goodOK := pods["r1"]
missing, missingOK := pods["r2"]
if !goodOK || !missingOK || body.RuntimeStatus != "ready" {
	t.Fatalf("runtime=%s pods=%+v warnings=%+v", body.RuntimeStatus, body.Pods, body.Warnings)
}
if good.CPUPercent <= 0 || good.MemoryPercent <= 0 || good.MemoryUsage == "" {
	t.Fatalf("successful stats were discarded: %+v", good)
}
if missing.CPUPercent != 0 || missing.MemoryPercent != 0 || missing.MemoryUsage != "" {
	t.Fatalf("failed pod should have empty metrics: %+v", missing)
}
warningFound := false
for _, warning := range body.Warnings {
	if strings.Contains(warning, "failed to read Docker stats") && strings.Contains(warning, "r2") {
		warningFound = true
	}
}
if !warningFound {
	t.Fatalf("missing partial stats warning: %+v", body.Warnings)
}
```

- [ ] **Step 2: Run the HTTP test and verify RED**

```powershell
go test ./internal/httpapi -run TestAIFARRuntimeStatsKeepsPartialMetricsWithoutDegradingRuntime -count=1
```

Expected: FAIL because the current `err == nil` branch discards the successful r1 stats when r2 returns 404.

- [ ] **Step 3: Map successes before appending the warning**

Replace the all-or-nothing branch in `buildAIFARRuntime` with:

```go
stats, statsErr := adapter.DockerContainerStatsForServer(ctx, server, names)
statsByName = mapStatsByName(stats)
if statsErr != nil {
	response.Warnings = append(response.Warnings, "failed to read Docker stats: "+statsErr.Error())
}
```

Do not set `RuntimeStatus` to degraded and do not modify readiness calculation.

- [ ] **Step 4: Run focused tests and commit**

```powershell
go test ./internal/httpapi -run 'TestAIFARRuntime(StatsKeepsPartialMetrics|CanSkipPodsAndStats|MarksRowsUnavailable)' -count=1
go test ./internal/adapter -run 'TestDocker(ContainerStatsForServer|APIContainerStats)' -count=1
git add backend/internal/httpapi/containers_aifar_runtime.go backend/internal/httpapi/containers_aifar_runtime_test.go
git commit -m "fix(runtime): preserve partial Docker stats"
```

Expected: all focused tests pass and only the two HTTP API files enter this commit.

---

### Task 4: Complete regression and scope verification

**Files:**
- Verify only; no new production files.

**Interfaces:**
- Consumes: Tasks 1-3.
- Produces: verification evidence and a clean feature worktree ready for review.

- [ ] **Step 1: Format all touched Go files**

```powershell
gofmt -w backend/internal/adapter/docker_api.go backend/internal/adapter/docker.go backend/internal/adapter/docker_stats_test.go backend/internal/httpapi/containers_aifar_runtime.go backend/internal/httpapi/containers_aifar_runtime_test.go
```

If formatting changes committed files, rerun focused tests and amend the responsible task commit.

- [ ] **Step 2: Run complete backend tests and build**

```powershell
pnpm test
pnpm backend:build
```

Expected: every Go package passes and both configured backend targets build successfully.

- [ ] **Step 3: Verify diff hygiene and scope**

```powershell
git diff --check
git status --short
git diff --name-only e4988b14..HEAD
```

Expected feature paths are limited to:

```text
backend/internal/adapter/docker.go
backend/internal/adapter/docker_api.go
backend/internal/adapter/docker_stats_test.go
backend/internal/httpapi/containers_aifar_runtime.go
backend/internal/httpapi/containers_aifar_runtime_test.go
docs/superpowers/plans/2026-07-27-docker-engine-stats-api.md
```

There must be no changes under `web/src`, `backend/cmd/aifar-agent`, store migrations, task types, or audit types. Generated binaries are not committed unless the user separately requests packaging.

- [ ] **Step 4: Record the real-target boundary**

Do not connect to or mutate a target server without separate authorization. If no live acceptance was authorized, report exactly:

```text
Real target not tested; automated httptest coverage proves the panel process does not execute Docker CLI for Engine API hosts.
```

If live acceptance is later authorized, record the target identifier, Docker API mode, displayed CPU/memory result, and confirmation that the panel host lacked a Docker executable. Never record credentials.
