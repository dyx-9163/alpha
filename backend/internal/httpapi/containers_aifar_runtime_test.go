package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

func TestAIFARRuntimeReturnsDegradedControlPlaneWhenAgentMissing(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			_ = json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer dockerAPI.Close()
	server, instance := seedAIFARRuntimeFixture(t, db, dockerAPI.URL)
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodGet, "/api/v2/containers/aifar/runtime?serverId="+server.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body aifarRuntimeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RuntimeStatus != "degraded" || body.Agent.Status != "missing" {
		t.Fatalf("expected degraded runtime with missing agent, got status=%s agent=%+v", body.RuntimeStatus, body.Agent)
	}
	if len(body.Instances) != 1 || body.Instances[0].ID != instance.ID || body.Instances[0].Legacy {
		t.Fatalf("unexpected instances: %+v", body.Instances)
	}
	if body.Instances[0].RuntimeConfig == nil || body.Instances[0].RuntimeConfig["global"] == nil {
		t.Fatalf("expected runtime config in instance response: %+v", body.Instances[0])
	}
	if len(body.Services) != 1 || body.Services[0].ServiceName != "permission" || body.Services[0].AppName != "alpha-permission" || body.Services[0].Status != "no-endpoints" {
		t.Fatalf("unexpected services: %+v", body.Services)
	}
}

func TestAIFARRuntimeCanSkipPodsAndStats(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	var containerListCalled atomic.Bool
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			containerListCalled.Store(true)
		}
		http.NotFound(w, r)
	}))
	defer dockerAPI.Close()
	server, _ := seedAIFARRuntimeFixture(t, db, dockerAPI.URL)
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodGet, "/api/v2/containers/aifar/runtime?serverId="+server.ID+"&includePods=0&includeStats=0", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if containerListCalled.Load() {
		t.Fatal("expected runtime summary request to skip Docker container calls")
	}
	var body aifarRuntimeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Pods) != 0 {
		t.Fatalf("expected pods to be omitted, got %+v", body.Pods)
	}
	if len(body.Deployments) != 1 || len(body.Services) != 1 {
		t.Fatalf("expected deployment and service summaries, got deployments=%+v services=%+v", body.Deployments, body.Services)
	}
}

func TestAIFARRuntimeMarksRowsUnavailableWhenDockerHostStopped(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	server, _ := seedAIFARRuntimeFixture(t, db, dockerAPI.URL)
	dockerAPI.Close()
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodGet, "/api/v2/containers/aifar/runtime?serverId="+server.ID+"&includePods=1&includeStats=0", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body aifarRuntimeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RuntimeStatus != "degraded" || len(body.Warnings) == 0 {
		t.Fatalf("expected degraded runtime with warning, got status=%s warnings=%+v", body.RuntimeStatus, body.Warnings)
	}
	if len(body.Deployments) != 1 || body.Deployments[0].Status != "no-endpoints" || body.Deployments[0].ReadyReplicas != 0 {
		t.Fatalf("expected deployment to lose ready replicas, got %+v", body.Deployments)
	}
	if len(body.Services) != 1 || body.Services[0].Status != "no-endpoints" || body.Services[0].ReadyEndpointCount != 0 || body.Services[0].ReadyReplicas != 0 {
		t.Fatalf("expected service endpoints to be unavailable, got %+v", body.Services)
	}
	if len(body.Pods) != 1 || body.Pods[0].Status != "stale" || body.Pods[0].Ready {
		t.Fatalf("expected stored pod to be stale when Docker is unavailable, got %+v", body.Pods)
	}
}

func TestAIFARRuntimeLogsAggregatesPodContainerLogs(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/containers/aifar-pod-admin-permission-rev-1-r1/logs" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("tail"); got != "20" {
			t.Fatalf("expected tail=20, got %q", got)
		}
		_, _ = w.Write([]byte("permission line 1\npermission line 2\n"))
	}))
	defer dockerAPI.Close()
	server, instance := seedAIFARRuntimeFixture(t, db, dockerAPI.URL)
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodGet, "/api/v2/containers/aifar/runtime/logs?serverId="+server.ID+"&instanceId="+instance.ID+"&service=permission&tail=20", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body aifarRuntimeLogsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ServerID != server.ID || body.InstanceID != instance.ID || body.Service != "permission" || body.Tail != 20 {
		t.Fatalf("unexpected runtime log response metadata: %+v", body)
	}
	if len(body.Pods) != 1 {
		t.Fatalf("expected one pod log group, got %+v", body.Pods)
	}
	got := strings.Join(body.Pods[0].Logs, "\n")
	if body.Pods[0].ServiceName != "permission" || body.Pods[0].ContainerName != "aifar-pod-admin-permission-rev-1-r1" || !strings.Contains(got, "permission line 2") {
		t.Fatalf("unexpected pod logs: %+v", body.Pods[0])
	}
}

func TestAIFARRuntimeLogQuerySupportsSelectionSetsAndDedup(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?service=file&services=oauth,permission&pods=pod-2,container-3&batch=5000&since=1710000000", nil)
	services := runtimeQueryValues(req, "service", "services")
	pods := runtimeQueryValues(req, "pod", "pods", "container", "containerName")
	if strings.Join(services, ",") != "file,oauth,permission" {
		t.Fatalf("unexpected service values: %+v", services)
	}
	if strings.Join(pods, ",") != "pod-2,container-3" {
		t.Fatalf("unexpected pod values: %+v", pods)
	}
	if got := boundedRuntimeLogBatch(queryInt(req, "batch", 200)); got != 1000 {
		t.Fatalf("expected batch to be capped at 1000, got %d", got)
	}
	if got := runtimeLogSinceFromRequest(req); !got.Equal(time.Unix(1710000000, 0)) {
		t.Fatalf("unexpected since value: %s", got)
	}

	filtered := filterRuntimeLogPods([]store.AIFARPod{
		{ServiceName: "file", PodID: "pod-1", ContainerName: "container-1"},
		{ServiceName: "oauth", PodID: "pod-2", ContainerName: "container-2"},
		{ServiceName: "permission", PodID: "pod-3", ContainerName: "container-3"},
		{ServiceName: "meeting", PodID: "pod-4", ContainerName: "container-4"},
	}, services, pods)
	if len(filtered) != 2 || filtered[0].ContainerName != "container-2" || filtered[1].ContainerName != "container-3" {
		t.Fatalf("unexpected filtered pods: %+v", filtered)
	}

	newLines, counts := runtimeLogNewLines([]string{"line-a", "line-b", "line-b", "line-c"}, map[string]int{"line-a": 1, "line-b": 1})
	if strings.Join(newLines, "|") != "line-b|line-c" {
		t.Fatalf("unexpected incremental lines: %+v", newLines)
	}
	if counts["line-a"] != 1 || counts["line-b"] != 2 || counts["line-c"] != 1 {
		t.Fatalf("unexpected line counts: %+v", counts)
	}

	overlapSince := time.Date(2026, 7, 7, 10, 0, 2, 0, time.UTC)
	recent := runtimeLogLinesSince([]string{
		"2026-07-07T10:00:01Z repeated",
		"2026-07-07T10:00:02Z repeated",
		"2026-07-07T10:00:03Z repeated",
		"line without timestamp",
	}, overlapSince)
	if strings.Join(recent, "|") != "2026-07-07T10:00:02Z repeated|2026-07-07T10:00:03Z repeated|line without timestamp" {
		t.Fatalf("unexpected overlap lines: %+v", recent)
	}

	if !runtimeLogInitialSince(time.Time{}).IsZero() {
		t.Fatal("expected empty log stream to start without local wall-clock since cursor")
	}
	nextSince := runtimeLogNextSince([]string{
		"2026-07-07T10:00:01Z same message",
		"2026-07-07T10:00:20Z same message",
	}, time.Unix(100, 0), time.Time{})
	if !nextSince.Equal(time.Date(2026, 7, 7, 10, 0, 17, 0, time.UTC)) {
		t.Fatalf("unexpected next since cursor: %s", nextSince)
	}
	floored := runtimeLogNextSince([]string{"2026-07-07T10:00:20Z line"}, time.Time{}, time.Date(2026, 7, 7, 10, 0, 19, 0, time.UTC))
	if !floored.Equal(time.Date(2026, 7, 7, 10, 0, 19, 0, time.UTC)) {
		t.Fatalf("expected cursor floor to win, got %s", floored)
	}
}

func TestAIFARRuntimeServiceSummaryPrunesResidualPodRecords(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	instance, err := db.SaveAppInstance(store.AppInstance{
		App:      "aifar",
		Version:  "runtime-v2",
		ServerID: "srv-1",
		Status:   "installed",
		Metadata: `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin","serviceCatalog":[{"name":"file","kind":"java","applicationName":"alpha-file","port":38005,"artifactExtensions":[".jar"],"healthPath":"/actuator/health/readiness","affinityPolicy":"stable"}]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARDeployment(store.AIFARDeployment{
		InstanceID:      instance.ID,
		ServiceName:     "file",
		DesiredReplicas: 2,
		CurrentRevision: "<nil>",
		Status:          "ready",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARReplicaSet(store.AIFARReplicaSet{
		InstanceID:  instance.ID,
		ServiceName: "file",
		Revision:    "rev-good",
		Image:       "aifar-file:rev-good",
		DesiredPods: 2,
		ReadyPods:   2,
		Status:      "ready",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARReplicaSet(store.AIFARReplicaSet{
		InstanceID:  instance.ID,
		ServiceName: "file",
		Revision:    "<nil>",
		Image:       "aifar-file:<nil>",
		DesiredPods: 2,
		ReadyPods:   2,
		Status:      "ready",
	}); err != nil {
		t.Fatal(err)
	}
	for _, pod := range []store.AIFARPod{
		{InstanceID: instance.ID, ServiceName: "file", Revision: "<nil>", PodID: "file--nil--r1", ContainerName: "aifar-pod-admin-file--nil--r1", Port: 38005, Status: "ready", Ready: true},
		{InstanceID: instance.ID, ServiceName: "file", Revision: "<nil>", PodID: "file--nil--r2", ContainerName: "aifar-pod-admin-file--nil--r2", Port: 38005, Status: "ready", Ready: true},
		{InstanceID: instance.ID, ServiceName: "file", Revision: "rev-good", PodID: "file-rev-good-r1", ContainerName: "aifar-pod-admin-file-rev-good-r1", Port: 38005, Status: "ready", Ready: true},
		{InstanceID: instance.ID, ServiceName: "file", Revision: "rev-good", PodID: "file-rev-good-r2", ContainerName: "aifar-pod-admin-file-rev-good-r2", Port: 38005, Status: "ready", Ready: true},
	} {
		if _, err := db.SaveAIFARPod(pod); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.ReplaceAIFARServiceEndpoints(instance.ID, "file", []store.AIFARServiceEndpoint{
		{InstanceID: instance.ID, ServiceName: "file", PodID: "file--nil--r1", ContainerName: "aifar-pod-admin-file--nil--r1", Revision: "<nil>", Port: 38005, State: "active", Ready: true},
		{InstanceID: instance.ID, ServiceName: "file", PodID: "file--nil--r2", ContainerName: "aifar-pod-admin-file--nil--r2", Revision: "<nil>", Port: 38005, State: "active", Ready: true},
	}); err != nil {
		t.Fatal(err)
	}

	response := aifarRuntimeResponse{RuntimeStatus: "ready", Agent: aifarRuntimeAgent{Status: "running"}}
	api.appendAIFARInstanceRuntime(&response, instance, map[string]adapter.DockerContainer{
		"aifar-pod-admin-file-rev-good-r1": {Name: "aifar-pod-admin-file-rev-good-r1", Image: "aifar-file:rev-good", State: "running", Status: "Up 1 minute (healthy)"},
		"aifar-pod-admin-file-rev-good-r2": {Name: "aifar-pod-admin-file-rev-good-r2", Image: "aifar-file:rev-good", State: "running", Status: "Up 1 minute (healthy)"},
	}, map[string]adapter.DockerContainerStat{}, aifarRuntimeBuildOptions{IncludePods: true})

	if len(response.Services) != 1 {
		t.Fatalf("expected one service, got %+v", response.Services)
	}
	if len(response.Deployments) != 1 || response.Deployments[0].DeploymentName != "alpha-file" || response.Deployments[0].PodRevision != "rev-good" {
		t.Fatalf("expected deployment identity and pod revision to be split, got %+v", response.Deployments)
	}
	service := response.Services[0]
	if service.AppName != "alpha-file" || service.Image != "aifar-file:rev-good" || service.ReadyReplicas != 2 || service.Status != "ready" {
		t.Fatalf("expected service summary to use real ready pods, got %+v", service)
	}
	for _, pod := range response.Pods {
		if strings.Contains(pod.ContainerName, "--nil--") {
			t.Fatalf("expected nil residual pod to be pruned from response, got %+v in %+v", pod, response.Pods)
		}
	}
	if len(response.Pods) != 2 {
		t.Fatalf("expected only real Docker pods in response, got %+v", response.Pods)
	}
	pods, err := db.ListAIFARPods(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, pod := range pods {
		if strings.Contains(pod.ContainerName, "--nil--") {
			t.Fatalf("expected nil residual pod to be pruned from store, got %+v", pods)
		}
	}
	endpoints, err := db.ListAIFARServiceEndpoints(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range endpoints {
		if strings.Contains(endpoint.ContainerName, "--nil--") {
			t.Fatalf("expected nil residual endpoint to be pruned from store, got %+v", endpoints)
		}
	}
}

func TestAIFARRuntimeReconcilesDockerPodsIntoControlPlane(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	revision := "20260708t074706.683351600z-services-im"
	containerName := "aifar-pod-admin-im-" + revision + "-r1"
	if _, err := db.SaveAIFARDeployment(store.AIFARDeployment{
		InstanceID:      instance.ID,
		ServiceName:     "im",
		DesiredReplicas: 0,
		CurrentRevision: revision,
		Status:          "offline",
	}); err != nil {
		t.Fatal(err)
	}

	response := aifarRuntimeResponse{RuntimeStatus: "ready", Agent: aifarRuntimeAgent{Status: "running"}}
	api.appendAIFARInstanceRuntime(&response, instance, map[string]adapter.DockerContainer{
		containerName: {
			Name:   containerName,
			Image:  "aifar-im:" + revision,
			State:  "running",
			Status: "Up 1 minute (healthy)",
			Labels: map[string]string{
				"aifar.app":          "aifar",
				"aifar.component":    "pod",
				"aifar.install-root": "/aifar/apps/admin",
				"aifar.service":      "im",
				"aifar.revision":     revision,
				"aifar.replica":      "1",
			},
		},
	}, map[string]adapter.DockerContainerStat{}, aifarRuntimeBuildOptions{IncludePods: true})

	var gotDeployment *aifarRuntimeDeployment
	for i := range response.Deployments {
		if response.Deployments[i].ServiceName == "im" {
			gotDeployment = &response.Deployments[i]
			break
		}
	}
	if gotDeployment == nil || gotDeployment.DesiredReplicas != 1 || gotDeployment.ReadyReplicas != 1 || gotDeployment.Status != "ready" {
		t.Fatalf("expected reconciled im deployment to be ready, got %+v in %+v", gotDeployment, response.Deployments)
	}
	var gotPod *aifarRuntimePod
	for i := range response.Pods {
		if response.Pods[i].ServiceName == "im" {
			gotPod = &response.Pods[i]
			break
		}
	}
	if gotPod == nil || gotPod.ContainerName != containerName || !gotPod.Ready || gotPod.Status != "ready" {
		t.Fatalf("expected reconciled im pod to be ready, got %+v in %+v", gotPod, response.Pods)
	}
	deployments, err := db.ListAIFARDeployments(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	var storedDeployment *store.AIFARDeployment
	for i := range deployments {
		if deployments[i].ServiceName == "im" {
			storedDeployment = &deployments[i]
			break
		}
	}
	if storedDeployment == nil || storedDeployment.DesiredReplicas != 1 || storedDeployment.Status != "ready" {
		t.Fatalf("expected im deployment to be persisted, got %+v in %+v", storedDeployment, deployments)
	}
	pods, err := db.ListAIFARPods(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundPod := false
	for _, pod := range pods {
		if pod.ServiceName == "im" && pod.ContainerName == containerName && pod.Ready {
			foundPod = true
		}
	}
	if !foundPod {
		t.Fatalf("expected im pod to be persisted, got %+v", pods)
	}
	endpoints, err := db.ListAIFARServiceEndpoints(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundEndpoint := false
	for _, endpoint := range endpoints {
		if endpoint.ServiceName == "im" && endpoint.ContainerName == containerName && endpoint.Ready && endpoint.State == "active" {
			foundEndpoint = true
		}
	}
	if !foundEndpoint {
		t.Fatalf("expected im endpoint to be persisted, got %+v", endpoints)
	}
	saved, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	metadata := runtimeMetadata(saved.Metadata)
	desired := runtimeDesiredReplicasFromMetadata(metadata)
	if desired["im"] != 1 {
		t.Fatalf("expected im desired replicas in metadata, got %+v metadata=%s", desired, saved.Metadata)
	}
	if !stringSet(runtimeServicesFromMetadata(metadata))["im"] {
		t.Fatalf("expected im to be present in metadata services, got %s", saved.Metadata)
	}
}

func TestAIFARRuntimeReconcilesDynamicServicePodsIntoControlPlane(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	metadata := runtimeMetadata(instance.Metadata)
	metadata["services"] = []string{"email"}
	metadata["desiredReplicas"] = map[string]any{"email": 1}
	metadata["serviceCatalog"] = []map[string]any{{
		"name":            "email",
		"kind":            "java",
		"applicationName": "alpha-email",
		"port":            38030,
		"healthPath":      "/actuator/health/readiness",
		"affinityPolicy":  "round-robin",
	}}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	instance.Metadata = string(raw)
	if _, err := db.SaveAppInstance(instance); err != nil {
		t.Fatal(err)
	}

	revision := "20260715t170859.998366100z-services-email"
	containerName := "aifar-pod-admin-email-" + revision + "-r1"
	if _, err := db.SaveAIFARDeployment(store.AIFARDeployment{
		InstanceID:      instance.ID,
		ServiceName:     "email",
		DesiredReplicas: 1,
		CurrentRevision: revision,
		Status:          "degraded",
	}); err != nil {
		t.Fatal(err)
	}

	response := aifarRuntimeResponse{RuntimeStatus: "ready", Agent: aifarRuntimeAgent{Status: "running"}}
	api.appendAIFARInstanceRuntime(&response, instance, map[string]adapter.DockerContainer{
		containerName: {
			Name:   containerName,
			Image:  "aifar-email:" + revision,
			State:  "running",
			Status: "Up 1 minute (healthy)",
			Ports:  "38030/tcp",
			Labels: map[string]string{
				"aifar.app":          "aifar",
				"aifar.component":    "pod",
				"aifar.install-root": "/aifar/apps/admin",
				"aifar.service":      "email",
				"aifar.revision":     revision,
				"aifar.replica":      "1",
			},
		},
	}, map[string]adapter.DockerContainerStat{}, aifarRuntimeBuildOptions{IncludePods: true})

	var gotPod *aifarRuntimePod
	for i := range response.Pods {
		if response.Pods[i].ServiceName == "email" {
			gotPod = &response.Pods[i]
			break
		}
	}
	if gotPod == nil || gotPod.ContainerName != containerName || !gotPod.Ready || gotPod.Port != 38030 {
		t.Fatalf("expected dynamic email pod to be reconciled with port 38030, got %+v in %+v", gotPod, response.Pods)
	}
	var gotService *aifarRuntimeService
	for i := range response.Services {
		if response.Services[i].ServiceName == "email" {
			gotService = &response.Services[i]
			break
		}
	}
	if gotService == nil || gotService.AppName != "alpha-email" {
		t.Fatalf("expected email service appName from serviceCatalog, got %+v in %+v", gotService, response.Services)
	}

	endpoints, err := db.ListAIFARServiceEndpoints(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range endpoints {
		if endpoint.ServiceName == "email" && endpoint.ContainerName == containerName && endpoint.Ready && endpoint.Port == 38030 {
			return
		}
	}
	t.Fatalf("expected dynamic email endpoint to be persisted with port 38030, got %+v", endpoints)
}

func TestAIFARRuntimeRequiresServiceCatalogForDockerDiscovery(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	server, err := db.SaveServer(store.Server{Name: "docker-1", Host: "10.0.0.10", DockerHost: "unix:///var/run/docker.sock", DeployDir: "/aifar/apps"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{
		App:      "aifar",
		Version:  "runtime-v2",
		ServerID: server.ID,
		Status:   "installed",
		Metadata: `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin","runtimeService":"aifar-agent"}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	containerName := "aifar-pod-admin-permission-rev-1-r1"
	response := aifarRuntimeResponse{RuntimeStatus: "ready", Agent: aifarRuntimeAgent{Status: "running"}}
	api.appendAIFARInstanceRuntime(&response, instance, map[string]adapter.DockerContainer{
		containerName: {
			Name:   containerName,
			Image:  "aifar-permission:rev-1",
			State:  "running",
			Status: "Up 1 minute (healthy)",
			Labels: map[string]string{
				"aifar.app":          "aifar",
				"aifar.component":    "pod",
				"aifar.install-root": "/aifar/apps/admin",
				"aifar.service":      "permission",
				"aifar.revision":     "rev-1",
				"aifar.replica":      "1",
			},
		},
	}, map[string]adapter.DockerContainerStat{}, aifarRuntimeBuildOptions{IncludePods: true})

	if len(response.Pods) != 0 || len(response.Deployments) != 0 || len(response.Services) != 0 {
		t.Fatalf("expected instance without serviceCatalog to skip docker discovery, got deployments=%+v pods=%+v services=%+v", response.Deployments, response.Pods, response.Services)
	}
	endpoints, err := db.ListAIFARServiceEndpoints(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 0 {
		t.Fatalf("expected no endpoints without serviceCatalog, got %+v", endpoints)
	}
}

func TestAIFARRuntimeReconcileHonorsMetadataDesiredWhenDeploymentIsStale(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	metadata := runtimeMetadata(instance.Metadata)
	metadata["desiredReplicas"] = map[string]any{"system": 1}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	instance.Metadata = string(raw)
	if _, err := db.SaveAppInstance(instance); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARDeployment(store.AIFARDeployment{
		InstanceID:      instance.ID,
		ServiceName:     "system",
		DesiredReplicas: 2,
		CurrentRevision: "rev-1",
		Status:          "degraded",
	}); err != nil {
		t.Fatal(err)
	}
	containerName := "aifar-pod-admin-system-rev-1-r1"
	response := aifarRuntimeResponse{RuntimeStatus: "ready", Agent: aifarRuntimeAgent{Status: "running"}}
	api.appendAIFARInstanceRuntime(&response, instance, map[string]adapter.DockerContainer{
		containerName: {
			Name:   containerName,
			Image:  "aifar-system:rev-1",
			State:  "running",
			Status: "Up 1 minute (healthy)",
			Labels: map[string]string{
				"aifar.app":          "aifar",
				"aifar.component":    "pod",
				"aifar.install-root": "/aifar/apps/admin",
				"aifar.service":      "system",
				"aifar.revision":     "rev-1",
				"aifar.replica":      "1",
			},
		},
	}, map[string]adapter.DockerContainerStat{}, aifarRuntimeBuildOptions{IncludePods: true})

	var gotDeployment *aifarRuntimeDeployment
	for i := range response.Deployments {
		if response.Deployments[i].ServiceName == "system" {
			gotDeployment = &response.Deployments[i]
			break
		}
	}
	if gotDeployment == nil || gotDeployment.DesiredReplicas != 1 || gotDeployment.ReadyReplicas != 1 || gotDeployment.Status != "ready" {
		t.Fatalf("expected stale deployment desired replicas to be corrected, got %+v in %+v", gotDeployment, response.Deployments)
	}
	deployments, err := db.ListAIFARDeployments(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, deployment := range deployments {
		if deployment.ServiceName == "system" {
			if deployment.DesiredReplicas != 1 || deployment.Status != "ready" {
				t.Fatalf("expected stored system deployment to be corrected, got %+v", deployment)
			}
			return
		}
	}
	t.Fatalf("expected system deployment row, got %+v", deployments)
}

func TestParseAIFARPodContainerNameSupportsRuntimeAgentNames(t *testing.T) {
	instanceName, service, revision, replica, ok := parseAIFARPodContainerNameWithServices("aifar-pod-admin-im-20260708t074706.683351600z-services-im-r1", []string{"im"})
	if !ok {
		t.Fatal("expected runtime agent pod container name to parse")
	}
	if instanceName != "admin" || service != "im" || revision != "20260708t074706.683351600z-services-im" || replica != 1 {
		t.Fatalf("unexpected parse result instance=%s service=%s revision=%s replica=%d", instanceName, service, revision, replica)
	}
}

func TestRuntimeIngressStatusUsesAgentListeners(t *testing.T) {
	metadata := map[string]any{
		"runtimeService":  "aifar-agent",
		"endpoint":        "10.0.0.10:8080",
		"gatewayEndpoint": "10.0.0.10:38000",
		"gatewayPort":     float64(38000),
		"webPort":         float64(8080),
	}
	row := runtimeIngressFromMetadata("inst-1", metadata, aifarRuntimeAgent{
		Status:    "running",
		Listeners: []int{8080},
	})
	if row.Status != "degraded" || !strings.Contains(row.Error, "38000") {
		t.Fatalf("expected ingress to be degraded when gateway listener is missing, got %+v", row)
	}

	row = runtimeIngressFromMetadata("inst-1", metadata, aifarRuntimeAgent{
		Status: "missing",
		Error:  "ssh credential is not available",
	})
	if row.Status != "missing" || !strings.Contains(row.Error, "ssh credential") {
		t.Fatalf("expected ingress to follow missing agent state, got %+v", row)
	}
}

func TestAIFARRuntimeScaleActionsRequireAgent(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			_ = json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer dockerAPI.Close()
	server, instance := seedAIFARRuntimeFixture(t, db, dockerAPI.URL)
	token := issueTestToken(t, db, secret, "owner", "owner")

	for _, path := range []string{
		"/api/v2/containers/aifar/services/permission/scale-out",
		"/api/v2/containers/aifar/services/permission/scale-in",
	} {
		req := httptest.NewRequest(http.MethodPost, path+"?serverId="+server.ID, strings.NewReader(`{"instanceId":"`+instance.ID+`"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		api.Router().ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("%s: expected 409, got %d body=%s", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "AIFAR_AGENT_REQUIRED") {
			t.Fatalf("%s: expected agent-required error, got %s", path, rec.Body.String())
		}
	}
}

func TestCurrentAIFARServiceDesiredReplicasReadsDeployment(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	if _, err := db.SaveAIFARDeployment(store.AIFARDeployment{
		InstanceID:      instance.ID,
		ServiceName:     "permission",
		DesiredReplicas: 3,
		CurrentRevision: "rev-1",
		Status:          "ready",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := api.currentAIFARServiceDesiredReplicas(instance.ID, "permission")
	if err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Fatalf("expected desired replicas 3, got %d", got)
	}
	if _, err := api.currentAIFARServiceDesiredReplicas(instance.ID, "missing"); err == nil {
		t.Fatal("expected missing deployment error")
	}
}

func TestAIFARRuntimeInstallServicesRequiresAgent(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			_ = json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer dockerAPI.Close()
	server, instance := seedAIFARRuntimeFixture(t, db, dockerAPI.URL)
	token := issueTestToken(t, db, secret, "owner", "owner")

	body := `{"instanceId":"` + instance.ID + `","services":["file"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v2/containers/aifar/services/install?serverId="+server.ID, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "AIFAR_AGENT_REQUIRED") {
		t.Fatalf("expected agent-required error, got %s", rec.Body.String())
	}
}

func TestAIFARRuntimeConfigRequiresAgent(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			_ = json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer dockerAPI.Close()
	server, instance := seedAIFARRuntimeFixture(t, db, dockerAPI.URL)
	token := issueTestToken(t, db, secret, "owner", "owner")

	body := `{"instanceId":"` + instance.ID + `","global":{"appCPUs":"3.0","appMemoryLimit":"3GB","jvmInitialRAMPercentage":25,"jvmMaxRAMPercentage":75}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v2/containers/aifar/runtime/config?serverId="+server.ID, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "AIFAR_AGENT_REQUIRED") {
		t.Fatalf("expected agent-required error, got %s", rec.Body.String())
	}
}

func TestAIFARRuntimeCleanupAndAgentUninstallStartTasksWithoutAgent(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	module := &fakeAIFARRuntimeActionModule{}
	api.apps = registry.New(module)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			_ = json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer dockerAPI.Close()
	server, instance := seedAIFARRuntimeFixture(t, db, dockerAPI.URL)
	token := issueTestToken(t, db, secret, "owner", "owner")

	for _, tc := range []struct {
		name   string
		path   string
		action string
	}{
		{name: "cleanup", path: "/api/v2/containers/aifar/runtime/cleanup-stale", action: "aifar.runtime.cleanup"},
		{name: "uninstall-agent", path: "/api/v2/containers/aifar/runtime/uninstall-agent", action: "aifar.agent.uninstall"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path+"?serverId="+server.ID, strings.NewReader(`{"instanceId":"`+instance.ID+`"}`))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			api.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusAccepted {
				t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			taskID, _ := body["taskId"].(string)
			if taskID == "" {
				t.Fatalf("expected taskId in response: %+v", body)
			}
			waitForTaskStatus(t, db, taskID, "success")
			assertAuditExists(t, db, tc.action, "running", "owner", instance.ID)
		})
	}
	if module.cleanupCalls != 1 || module.uninstallCalls != 1 {
		t.Fatalf("expected cleanup and uninstall module calls, got cleanup=%d uninstall=%d", module.cleanupCalls, module.uninstallCalls)
	}
}

func TestAIFARRuntimeRestartAllCreatesPlannedTaskForSelectedInstance(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	module := &fakeAIFARRuntimeActionModule{}
	api.apps = registry.New(module)
	api.aifarAgentStatus = func(context.Context, store.Server) aifarRuntimeAgent {
		return aifarRuntimeAgent{Status: "running", Features: []string{"restart-runtime"}}
	}
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer dockerAPI.Close()
	server, instance := seedAIFARRuntimeFixture(t, db, dockerAPI.URL)
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodPost, "/api/v2/containers/aifar/runtime/restart-all?serverId="+server.ID, strings.NewReader(`{"instanceId":"`+instance.ID+`","reason":"load new env"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	taskID := decodeTaskID(t, rec)
	task, _, err := db.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Type != "aifar.runtime.restart-all" || task.Target != instance.ID {
		t.Fatalf("unexpected restart task: %+v", task)
	}
	steps, err := db.ListTaskSteps(taskID)
	if err != nil {
		t.Fatal(err)
	}
	wantSteps := []string{"load-instance", "preflight-runtime", "rolling-restart", "verify-runtime"}
	if len(steps) != len(wantSteps) {
		t.Fatalf("expected %d steps, got %+v", len(wantSteps), steps)
	}
	for index, want := range wantSteps {
		if steps[index].Name != want || steps[index].Target != server.ID {
			t.Fatalf("unexpected step %d: %+v", index, steps[index])
		}
	}
	targets, err := db.ListTaskTargets(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Target != server.ID {
		t.Fatalf("restart must target only selected instance server: %+v", targets)
	}
	waitForTaskStatus(t, db, taskID, "success")
	steps, err = db.ListTaskSteps(taskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if step.Status != "success" {
			t.Fatalf("expected completed restart steps, got %+v", steps)
		}
	}
	targets, err = db.ListTaskTargets(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Status != "success" {
		t.Fatalf("expected successful restart target, got %+v", targets)
	}
	if module.restartCalls != 1 || module.restartRequest.Instance.ID != instance.ID || module.restartRequest.Reason != "load new env" {
		t.Fatalf("unexpected restart module call: calls=%d request=%+v", module.restartCalls, module.restartRequest)
	}
	assertAuditExists(t, db, "containers.aifar.runtime.restart-all", "running", "owner", instance.ID)
}

func TestAIFARRuntimeRestartAllRequiresRunningAgent(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	api.apps = registry.New(&fakeAIFARRuntimeActionModule{})
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer dockerAPI.Close()
	server, instance := seedAIFARRuntimeFixture(t, db, dockerAPI.URL)
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodPost, "/api/v2/containers/aifar/runtime/restart-all?serverId="+server.ID, strings.NewReader(`{"instanceId":"`+instance.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "AIFAR_AGENT_REQUIRED") {
		t.Fatalf("expected agent-required conflict, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAIFARRuntimeRestartAllRequiresAppsManagePermission(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer dockerAPI.Close()
	server, instance := seedAIFARRuntimeFixture(t, db, dockerAPI.URL)
	token := issueTestToken(t, db, secret, "viewer", "viewer")

	req := httptest.NewRequest(http.MethodPost, "/api/v2/containers/aifar/runtime/restart-all?serverId="+server.ID, strings.NewReader(`{"instanceId":"`+instance.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

type fakeAIFARRuntimeActionModule struct {
	cleanupCalls   int
	uninstallCalls int
	restartCalls   int
	restartRequest registry.RuntimeRestartRequest
	restartErr     error
}

func (m *fakeAIFARRuntimeActionModule) Name() string { return "aifar" }

func (m *fakeAIFARRuntimeActionModule) Manifest(lang string) registry.Manifest {
	return registry.Manifest{Name: "aifar", BackendReady: true}
}

func (m *fakeAIFARRuntimeActionModule) PreflightInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) (registry.PreflightResult, error) {
	return registry.PreflightResult{}, nil
}

func (m *fakeAIFARRuntimeActionModule) PlanInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) ([]registry.InstallStepPlan, error) {
	return nil, nil
}

func (m *fakeAIFARRuntimeActionModule) ValidateInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) error {
	return nil
}

func (m *fakeAIFARRuntimeActionModule) Install(ctx context.Context, req registry.InstallRequest, run registry.RunContext) error {
	return nil
}

func (m *fakeAIFARRuntimeActionModule) CleanupRuntimeStalePods(ctx context.Context, req registry.RuntimeCleanupRequest, run registry.RunContext) error {
	m.cleanupCalls++
	return nil
}

func (m *fakeAIFARRuntimeActionModule) UninstallRuntimeAgent(ctx context.Context, req registry.RuntimeAgentUninstallRequest, run registry.RunContext) error {
	m.uninstallCalls++
	return nil
}

func (m *fakeAIFARRuntimeActionModule) RestartRuntime(ctx context.Context, req registry.RuntimeRestartRequest, run registry.RunContext) error {
	m.restartCalls++
	m.restartRequest = req
	target := req.Server.ID
	if recorder, ok := run.Log.(interface {
		StartTarget(string)
		FinishTarget(string, string, string)
		StartStep(string, string, string, int)
		FinishStep(string, string, string, string)
	}); ok {
		recorder.StartTarget(target)
		steps := []string{"load-instance", "preflight-runtime", "rolling-restart", "verify-runtime"}
		for index, name := range steps {
			recorder.StartStep(target, name, name, index+1)
			if m.restartErr != nil && name == "rolling-restart" {
				recorder.FinishStep(target, name, "failed", m.restartErr.Error())
				recorder.FinishTarget(target, "failed", m.restartErr.Error())
				return m.restartErr
			}
			recorder.FinishStep(target, name, "success", "")
		}
		recorder.FinishTarget(target, "success", "")
		return nil
	}
	return m.restartErr
}

func seedAIFARRuntimeFixture(t *testing.T, db *store.Store, dockerHost string) (store.Server, store.AppInstance) {
	t.Helper()
	server, err := db.SaveServer(store.Server{Name: "docker-1", Host: "10.0.0.10", DockerHost: dockerHost, DeployDir: "/aifar/apps"})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(map[string]any{
		"orchestrationModel": "agent-runtime-v2",
		"installRoot":        "/aifar/apps/admin",
		"endpoint":           "10.0.0.10:8080",
		"gatewayEndpoint":    "10.0.0.10:38000",
		"gatewayPort":        38000,
		"webPort":            8080,
		"runtimeService":     "aifar-agent",
		"serviceCatalog": []map[string]any{
			{"name": "permission", "kind": "java", "applicationName": "alpha-permission", "port": 38010, "artifactExtensions": []string{".jar"}, "healthPath": "/actuator/health/readiness", "affinityPolicy": "round-robin"},
			{"name": "im", "kind": "java", "applicationName": "alpha-im", "port": 38031, "artifactExtensions": []string{".jar"}, "healthPath": "/actuator/health/readiness", "affinityPolicy": "round-robin"},
			{"name": "system", "kind": "java", "applicationName": "alpha-system", "port": 38002, "artifactExtensions": []string{".jar"}, "healthPath": "/actuator/health/readiness", "affinityPolicy": "round-robin"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{
		App:      "aifar",
		Version:  "runtime-v2",
		ServerID: server.ID,
		Status:   "installed",
		Metadata: string(metadata),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARDeployment(store.AIFARDeployment{
		InstanceID:      instance.ID,
		ServiceName:     "permission",
		DesiredReplicas: 1,
		CurrentRevision: "rev-1",
		Status:          "ready",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARReplicaSet(store.AIFARReplicaSet{
		InstanceID:  instance.ID,
		ServiceName: "permission",
		Revision:    "rev-1",
		Image:       "aifar-permission:rev-1",
		DesiredPods: 1,
		ReadyPods:   1,
		Status:      "ready",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARPod(store.AIFARPod{
		InstanceID:    instance.ID,
		ServiceName:   "permission",
		Revision:      "rev-1",
		PodID:         "r1",
		ContainerName: "aifar-pod-admin-permission-rev-1-r1",
		Port:          38010,
		Status:        "running",
		Ready:         true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceAIFARServiceEndpoints(instance.ID, "permission", []store.AIFARServiceEndpoint{{
		InstanceID:    instance.ID,
		ServiceName:   "permission",
		PodID:         "r1",
		ContainerName: "aifar-pod-admin-permission-rev-1-r1",
		Revision:      "rev-1",
		Port:          38010,
		State:         "active",
		Ready:         true,
	}}); err != nil {
		t.Fatal(err)
	}
	return server, instance
}
