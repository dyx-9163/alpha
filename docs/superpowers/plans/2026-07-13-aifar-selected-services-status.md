# AIFAR Selected Services Status Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make AIFAR instance health checks evaluate only selected services whose desired replica count is greater than zero.

**Architecture:** Keep `app.instance` as the App Store status source, but make the AIFAR `CheckModule` derive a deterministic service expectation list from instance metadata and pass it into the remote inspector. The inspector compares each service's desired replica count with its own running healthy containers, so extra replicas of one service cannot hide a missing replica of another service.

**Tech Stack:** Go 1.24, existing AIFAR app module, SSH shell status probe, Go `testing`, pnpm build/test wrappers.

## Global Constraints

- Treat `desiredReplicas` as authoritative when the metadata key is present.
- Ignore unselected services and selected services with `desiredReplicas = 0`.
- Fall back to `services` with one expected replica each only when `desiredReplicas` is absent; use the legacy default list only when both fields are absent.
- Preserve the existing task, collector, SSE, API, database, and frontend contracts.
- Do not accept free-form shell input; quote the backend-generated expectation assignment string.
- Preserve all unrelated staged and unstaged workspace changes.
- Do not create an implementation commit because `service.go` and `service_test.go` already contain overlapping user changes; deliver a reviewed working-tree diff instead.

---

### Task 1: Derive and enforce selected-service expectations

**Files:**
- Modify: `backend/internal/apps/aifar/status.go`
- Modify: `backend/internal/apps/aifar/service.go:1699-1762`
- Test: `backend/internal/apps/aifar/service_test.go`

**Interfaces:**
- Consumes: decoded instance metadata from `metadataFromInstance`, `servicesFromMetadata`, and `desiredReplicasFromMetadata`.
- Produces: `serviceExpectations(metadata map[string]any) []serviceExpectation` and `Inspector.Check(ctx, server, installRoot, expectations, log)`.

- [ ] **Step 1: Write failing expectation-selection tests**

Add tests that express the authoritative and fallback rules before adding production code:

```go
func TestServiceExpectationsUseOnlyPositiveDesiredReplicas(t *testing.T) {
	metadata := map[string]any{
		"services": []any{"alpha-gateway", "alpha-oauth", "web-vue3", "alpha-unused"},
		"desiredReplicas": map[string]any{
			"alpha-gateway": 1,
			"alpha-oauth":   2,
			"web-vue3":      1,
			"alpha-unused":  0,
		},
	}
	got := serviceExpectations(metadata)
	want := []serviceExpectation{
		{Name: "alpha-gateway", Replicas: 1},
		{Name: "alpha-oauth", Replicas: 2},
		{Name: "web-vue3", Replicas: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected selected positive replicas %#v, got %#v", want, got)
	}
}

func TestServiceExpectationsTreatExplicitEmptyDesiredReplicasAsOffline(t *testing.T) {
	metadata := map[string]any{
		"services":        []any{"alpha-gateway", "web-vue3"},
		"desiredReplicas": map[string]any{},
	}
	if got := serviceExpectations(metadata); len(got) != 0 {
		t.Fatalf("explicit empty desired replicas should be offline, got %#v", got)
	}
}

func TestServiceExpectationsFallBackToSelectedServices(t *testing.T) {
	metadata := map[string]any{"services": []any{"alpha-gateway", "web-vue3"}}
	want := []serviceExpectation{{Name: "alpha-gateway", Replicas: 1}, {Name: "web-vue3", Replicas: 1}}
	if got := serviceExpectations(metadata); !reflect.DeepEqual(got, want) {
		t.Fatalf("expected selected-service fallback %#v, got %#v", want, got)
	}
}
```

Add `reflect` to the existing test imports if it is not already present.

- [ ] **Step 2: Run the focused tests and confirm RED**

Run:

```powershell
go test ./internal/apps/aifar -run TestServiceExpectations -count=1
```

Expected: compilation fails because `serviceExpectation` and `serviceExpectations` do not exist.

- [ ] **Step 3: Implement deterministic expectation derivation**

In `status.go`, add:

```go
type serviceExpectation struct {
	Name     string
	Replicas int
}

func serviceExpectations(metadata map[string]any) []serviceExpectation {
	_, desiredDeclared := metadata["desiredReplicas"]
	desiredReplicas := desiredReplicasFromMetadata(metadata)
	selected := servicesFromMetadata(metadata)
	if !desiredDeclared {
		if len(selected) == 0 {
			selected = append([]string(nil), serviceOrder...)
		}
		out := make([]serviceExpectation, 0, len(selected))
		seen := map[string]struct{}{}
		for _, name := range selected {
			name = cleanAIFARServiceName(name)
			if name == "" || !aifarServiceSupported(name) {
				continue
			}
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, serviceExpectation{Name: name, Replicas: 1})
		}
		return out
	}

	out := make([]serviceExpectation, 0, len(desiredReplicas))
	seen := map[string]struct{}{}
	appendExpected := func(name string) {
		name = cleanAIFARServiceName(name)
		replicas := desiredReplicas[name]
		if name == "" || replicas <= 0 || !aifarServiceSupported(name) {
			return
		}
		if _, exists := seen[name]; exists {
			return
		}
		seen[name] = struct{}{}
		out = append(out, serviceExpectation{Name: name, Replicas: replicas})
	}
	for _, name := range selected {
		appendExpected(name)
	}
	extra := make([]string, 0, len(desiredReplicas))
	for name := range desiredReplicas {
		if _, exists := seen[cleanAIFARServiceName(name)]; !exists {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		appendExpected(name)
	}
	return out
}
```

Add `sort` to `status.go` imports. Keep the explicit metadata-key check so `{}` means all services are offline rather than legacy fallback.

- [ ] **Step 4: Run the focused tests and confirm GREEN**

Run:

```powershell
go test ./internal/apps/aifar -run TestServiceExpectations -count=1
```

Expected: PASS.

- [ ] **Step 5: Write failing status-command tests**

Add tests proving dynamic selection, zero-replica exclusion, and per-service missing counts:

```go
func TestStatusCommandChecksOnlyExpectedDynamicServices(t *testing.T) {
	command := statusCommand("/aifar/apps/admin", []serviceExpectation{
		{Name: "alpha-gateway", Replicas: 1},
		{Name: "alpha-oauth", Replicas: 2},
		{Name: "web-vue3", Replicas: 1},
	})
	for _, want := range []string{
		`EXPECTED_SERVICES='alpha-gateway=1 alpha-oauth=2 web-vue3=1'`,
		`MISSING=$((MISSING + desired - service_healthy))`,
		`[ "$MISSING" -eq 0 ]`,
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("status command should contain %q:\n%s", want, command)
		}
	}
	if strings.Contains(command, "permission=") || strings.Contains(command, "message=") {
		t.Fatalf("status command should not inspect unselected services:\n%s", command)
	}
}

func TestParseStatusOutputIncludesExpectedAndMissingContainers(t *testing.T) {
	status := parseStatusOutput("status=degraded\nexpectedContainers=5\nmissingContainers=1\n")
	if status.ExpectedContainers != 5 || status.MissingContainers != 1 {
		t.Fatalf("expected replica diagnostics, got %+v", status)
	}
}
```

Update existing calls in `TestStatusCommandScansK8sLikePodsAndAgentRuntime` to pass `serviceExpectations(nil)`.

- [ ] **Step 6: Run the focused command tests and confirm RED**

Run:

```powershell
go test ./internal/apps/aifar -run 'Test(StatusCommand|ParseStatusOutputIncludesExpected)' -count=1
```

Expected: compilation fails because `statusCommand` still accepts one argument and `StatusResult` lacks the diagnostic fields.

- [ ] **Step 7: Implement the selected-service remote probe**

Extend `StatusResult`:

```go
ExpectedContainers int
MissingContainers  int
```

Change the inspector signature and command call:

```go
func (i Inspector) Check(ctx context.Context, server store.Server, installRoot string, expectations []serviceExpectation, log Logger) (StatusResult, error) {
	// keep install-root normalization
	result, err := i.remote.Run(ctx, server, statusCommand(installRoot, expectations))
	// keep existing logging and parsing
}
```

Change `statusCommand` to accept `[]serviceExpectation`, render a shell-quoted assignment string such as `alpha-gateway=1 alpha-oauth=2`, and replace the fixed `serviceOrderText()` loop with:

```sh
EXPECTED=0
MISSING=0
for entry in $EXPECTED_SERVICES; do
  service="${entry%%=*}"
  desired="${entry#*=}"
  EXPECTED=$((EXPECTED + desired))
  service_healthy=0
  names="$(docker ps -a --filter "label=aifar.app=aifar" --filter "label=aifar.install-root=$INSTALL_ROOT" --filter "label=aifar.component=pod" --filter "label=aifar.service=$service" --format '{{.Names}}' 2>/dev/null || true)"
  for name in $names; do
    # retain current TOTAL, RUNNING, UNHEALTHY, revision and CONTAINERS collection
    if [ "$running" = "true" ] && [ "$health" != "unhealthy" ]; then
      service_healthy=$((service_healthy + 1))
    fi
  done
  if [ "$service_healthy" -lt "$desired" ]; then
    MISSING=$((MISSING + desired - service_healthy))
  fi
done
```

Use this terminal decision:

```sh
if [ "$EXPECTED" -eq 0 ]; then
  STATUS="offline"
elif [ "$MISSING" -eq 0 ] && [ "$UNHEALTHY" -eq 0 ] && [ "$INGRESS_RUNNING" = "true" ]; then
  STATUS="running"
elif [ "$RUNNING" -gt 0 ]; then
  STATUS="degraded"
else
  STATUS="stopped"
fi
```

Print and parse both diagnostics:

```sh
echo "expectedContainers=$EXPECTED"
echo "missingContainers=$MISSING"
```

```go
case "expectedContainers":
	result.ExpectedContainers = atoi(value)
case "missingContainers":
	result.MissingContainers = atoi(value)
```

- [ ] **Step 8: Wire instance metadata into `Service.Check`**

Replace the current inspector call inside step 1 with:

```go
expectations := serviceExpectations(metadata)
status, checkErr = NewInspector(s.remote).Check(ctx, req.Server, installRoot, expectations, logForServer)
```

Add the diagnostic fields to persisted `lastCheck` details:

```go
"expectedContainers": status.ExpectedContainers,
"missingContainers":  status.MissingContainers,
```

- [ ] **Step 9: Run focused tests and confirm GREEN**

Run:

```powershell
go test ./internal/apps/aifar -run 'Test(ServiceExpectations|StatusCommand|ParseStatus|ServiceChecks)' -count=1
```

Expected: PASS.

- [ ] **Step 10: Run package and repository verification**

Run in order:

```powershell
go test ./internal/apps/aifar -count=1
pnpm test
pnpm backend:build
git -c safe.directory=D:/workspace/aifar-deployment diff --check
```

Expected: every command exits 0. If the known Collector timing assertion fails during `pnpm test`, rerun that package once with `go test ./internal/collector -count=1`, report both outputs, and do not claim the full suite passed unless the fresh full command exits 0.

- [ ] **Step 11: Review the final diff without committing overlapping user changes**

Run:

```powershell
git -c safe.directory=D:/workspace/aifar-deployment diff -- backend/internal/apps/aifar/status.go backend/internal/apps/aifar/service.go backend/internal/apps/aifar/service_test.go memory.md
```

Confirm the diff contains only the approved selected-service status behavior on top of the existing directory-driven work. Leave the implementation changes in the working tree so pre-existing edits in the same files are not accidentally committed together.
