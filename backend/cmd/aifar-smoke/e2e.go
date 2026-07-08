package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"aifar-deployment/backend/internal/config"
	"aifar-deployment/backend/internal/httpapi"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"
)

const (
	defaultE2ETaskTimeout    = 120 * time.Minute
	defaultE2ERuntimeTimeout = 10 * time.Minute
	defaultE2EPollInterval   = 2 * time.Second
)

type e2eStageReport struct {
	App           string    `json:"app"`
	Action        string    `json:"action"`
	TaskID        string    `json:"taskId,omitempty"`
	Status        string    `json:"status"`
	StartedAt     time.Time `json:"startedAt"`
	FinishedAt    time.Time `json:"finishedAt"`
	DurationMS    int64     `json:"durationMs"`
	InstanceIDs   []string  `json:"instanceIds,omitempty"`
	CleanupStatus string    `json:"cleanupStatus,omitempty"`
	Message       string    `json:"message,omitempty"`
}

type e2eAPICheckReport struct {
	Name       string `json:"name"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     string `json:"status"`
	HTTPStatus int    `json:"httpStatus,omitempty"`
	DurationMS int64  `json:"durationMs"`
	Message    string `json:"message,omitempty"`
}

type e2eRemoteCheck struct {
	Name     string         `json:"name"`
	ServerID string         `json:"serverId"`
	Status   string         `json:"status"`
	Message  string         `json:"message,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
}

type e2eConfig struct {
	AllowMutation  bool
	DatabasePath   string
	OutputRoot     string
	ServerIDs      []string
	PrimaryServer  string
	Username       string
	Password       string
	AppPassword    string
	BaseURL        string
	TaskTimeout    time.Duration
	RuntimeTimeout time.Duration
	PollInterval   time.Duration
	KeepResources  bool
	DockerCleanup  string
}

type e2eRunner struct {
	cfg             e2eConfig
	report          *report
	client          *e2eHTTPClient
	lookup          *store.Store
	closeFunc       func()
	serverPasswords map[string]string
	created         map[string]store.AppInstance
	createdOrder    []string
	nacosInstanceID string
}

type e2eHTTPClient struct {
	baseURL string
	token   string
	client  *http.Client
	checks  *[]e2eAPICheckReport
}

type e2eInstallSpec struct {
	App       string
	Targets   []string
	Request   map[string]any
	Check     bool
	AfterHook func(*e2eRunner, []string)
}

type taskStartResponse struct {
	TaskID string `json:"taskId"`
	Status string `json:"status"`
}

type taskDetailResponse struct {
	Task    store.Task         `json:"task"`
	Logs    []store.TaskLog    `json:"logs"`
	Targets []store.TaskTarget `json:"targets"`
	Steps   []store.TaskStep   `json:"steps"`
}

type loginResponse struct {
	Token string `json:"token"`
	User  any    `json:"user"`
}

type dockerSummaryAPIResponse struct {
	Available bool           `json:"available"`
	Error     string         `json:"error,omitempty"`
	Summary   map[string]any `json:"summary,omitempty"`
}

type aifarRuntimeAPIResponse struct {
	ServerID      string `json:"serverId"`
	RuntimeStatus string `json:"runtimeStatus"`
	Services      []struct {
		InstanceID      string `json:"instanceId"`
		ServiceName     string `json:"serviceName"`
		DesiredReplicas int    `json:"desiredReplicas"`
		ReadyReplicas   int    `json:"readyReplicas"`
		ActiveEndpoints int    `json:"activeEndpoints"`
		Status          string `json:"status"`
		FailureReason   string `json:"failureReason,omitempty"`
		LastError       string `json:"lastError,omitempty"`
	} `json:"services"`
}

func runE2EMutatingScenario(args []string) int {
	baseCfg := config.Load()
	flags := flag.NewFlagSet("e2e-mutating", flag.ContinueOnError)
	databasePath := flags.String("database", baseCfg.DatabasePath, "SQLite database path")
	outputRoot := flags.String("output-dir", defaultOutputRoot(), "directory for JSON smoke reports")
	serverIDsFlag := flags.String("server-ids", os.Getenv("AIFAR_E2E_SERVER_IDS"), "comma-separated test server ID whitelist")
	baseURLFlag := flags.String("base-url", os.Getenv("AIFAR_E2E_BASE_URL"), "optional running AIFAR control-plane base URL")
	taskTimeout := flags.Duration("task-timeout", envDuration("AIFAR_E2E_TASK_TIMEOUT", defaultE2ETaskTimeout), "task wait timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	e2eCfg := e2eConfig{
		AllowMutation:  os.Getenv("AIFAR_E2E_ALLOW_MUTATION") == "1",
		DatabasePath:   *databasePath,
		OutputRoot:     *outputRoot,
		ServerIDs:      splitCSV(*serverIDsFlag),
		Username:       strings.TrimSpace(os.Getenv("AIFAR_E2E_USERNAME")),
		Password:       os.Getenv("AIFAR_E2E_PASSWORD"),
		AppPassword:    os.Getenv("AIFAR_E2E_APP_PASSWORD"),
		BaseURL:        strings.TrimRight(strings.TrimSpace(*baseURLFlag), "/"),
		TaskTimeout:    *taskTimeout,
		RuntimeTimeout: envDuration("AIFAR_E2E_RUNTIME_TIMEOUT", defaultE2ERuntimeTimeout),
		PollInterval:   envDuration("AIFAR_E2E_POLL_INTERVAL", defaultE2EPollInterval),
		KeepResources:  os.Getenv("AIFAR_E2E_KEEP_RESOURCES") == "1",
		DockerCleanup:  normalizeDockerCleanup(os.Getenv("AIFAR_E2E_DOCKER_CLEANUP")),
	}
	if len(e2eCfg.ServerIDs) > 0 {
		e2eCfg.PrimaryServer = e2eCfg.ServerIDs[0]
	}

	rep := newReport("e2e-mutating", e2eCfg.DatabasePath)
	if db, err := store.OpenReadOnlyWithSecret(e2eCfg.DatabasePath, baseCfg.CredentialSecret); err == nil {
		_ = collectInventory(db, &rep)
		_ = db.Close()
	}
	rep.Guard = &mutatingGuardReport{
		AllowMutation: e2eCfg.AllowMutation,
		ServerIDs:     e2eCfg.ServerIDs,
	}
	if problems := validateE2EConfig(e2eCfg); len(problems) > 0 {
		rep.Guard.Message = strings.Join(problems, "; ")
		rep.Failures = append(rep.Failures, problems...)
		rep.FinishedAt = time.Now()
		_ = writeReport(e2eCfg.OutputRoot, &rep)
		printReport(rep)
		return 1
	}
	rep.Guard.Message = "guard passed; mutating E2E is enabled"

	var cleanup func()
	runner, err := newE2ERunner(baseCfg, e2eCfg, &rep)
	if err != nil {
		rep.Failures = append(rep.Failures, cleanErrText(err.Error()))
		rep.FinishedAt = time.Now()
		_ = writeReport(e2eCfg.OutputRoot, &rep)
		printReport(rep)
		return 1
	}
	cleanup = runner.close
	defer cleanup()

	err = runner.Run(context.Background())
	if err != nil {
		rep.Failures = append(rep.Failures, cleanErrText(err.Error()))
	}
	if runner.lookup != nil {
		_ = collectInventory(runner.lookup, &rep)
	}
	rep.Passed = err == nil && len(rep.Failures) == 0
	rep.FinishedAt = time.Now()
	if writeErr := writeReport(e2eCfg.OutputRoot, &rep); writeErr != nil {
		fmt.Fprintf(os.Stderr, "write smoke report failed: %v\n", writeErr)
		rep.Passed = false
	}
	printReport(rep)
	if rep.Passed {
		return 0
	}
	return 1
}

func newE2ERunner(baseCfg config.Config, e2eCfg e2eConfig, rep *report) (*e2eRunner, error) {
	var (
		lookup *store.Store
		ts     *httptest.Server
	)
	if e2eCfg.BaseURL == "" {
		db, err := store.OpenWithSecret(e2eCfg.DatabasePath, baseCfg.CredentialSecret)
		if err != nil {
			return nil, fmt.Errorf("open writable database for in-process E2E API: %w", err)
		}
		tasks := worker.NewManagerWithConcurrency(db, baseCfg.DeploymentConcurrency)
		api := httpapi.New(baseCfg, db, tasks)
		ts = httptest.NewServer(api.Router())
		e2eCfg.BaseURL = ts.URL
		lookup = db
	} else {
		db, err := store.OpenReadOnlyWithSecret(e2eCfg.DatabasePath, baseCfg.CredentialSecret)
		if err != nil {
			return nil, fmt.Errorf("open read-only database for E2E lookup: %w", err)
		}
		lookup = db
	}

	runner := &e2eRunner{
		cfg:    e2eCfg,
		report: rep,
		client: &e2eHTTPClient{
			baseURL: e2eCfg.BaseURL,
			client:  &http.Client{Timeout: 60 * time.Second},
			checks:  &rep.APIChecks,
		},
		lookup:          lookup,
		serverPasswords: map[string]string{},
		created:         map[string]store.AppInstance{},
	}
	runner.closeFunc = func() {
		if ts != nil {
			ts.Close()
		}
		if lookup != nil {
			_ = lookup.Close()
		}
	}
	return runner, nil
}

func (r *e2eRunner) close() {
	if r.closeFunc != nil {
		r.closeFunc()
	}
}

func (r *e2eRunner) Run(ctx context.Context) error {
	if err := r.login(ctx); err != nil {
		return err
	}
	if err := r.preflight(ctx); err != nil {
		return err
	}
	runErr := r.installScenario(ctx)
	cleanupErr := r.cleanup(ctx)
	return errors.Join(runErr, cleanupErr)
}

func (r *e2eRunner) login(ctx context.Context) error {
	var out loginResponse
	if err := r.client.do(ctx, "login", http.MethodPost, "/api/v2/auth/login", map[string]any{
		"username": r.cfg.Username,
		"password": r.cfg.Password,
	}, &out); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}
	if strings.TrimSpace(out.Token) == "" {
		return errors.New("login response did not include token")
	}
	r.client.token = out.Token
	return nil
}

func (r *e2eRunner) preflight(ctx context.Context) error {
	var servers []store.Server
	if err := r.client.do(ctx, "list servers", http.MethodGet, "/api/v2/servers", nil, &servers); err != nil {
		return err
	}
	serverSet := map[string]bool{}
	for _, server := range servers {
		serverSet[server.ID] = true
	}
	for _, id := range r.cfg.ServerIDs {
		if !serverSet[id] {
			return fmt.Errorf("E2E server %s was not found", id)
		}
		server, err := r.lookup.GetServer(id, true)
		if err != nil {
			return fmt.Errorf("load E2E server %s secret: %w", id, err)
		}
		r.serverPasswords[id] = strings.TrimSpace(server.Password)
	}
	if !r.cfg.KeepResources {
		if strings.TrimSpace(r.serverPasswords[r.cfg.PrimaryServer]) == "" {
			return fmt.Errorf("primary server %s has no saved password; delete API requires serverPassword confirmation", r.cfg.PrimaryServer)
		}
		if r.cfg.DockerCleanup != "" {
			for _, id := range r.cfg.ServerIDs {
				if strings.TrimSpace(r.serverPasswords[id]) == "" {
					return fmt.Errorf("server %s has no saved password; Docker cleanup requires serverPassword confirmation", id)
				}
			}
		}
	}

	var resources []store.Resource
	if err := r.client.do(ctx, "list resources", http.MethodGet, "/api/v2/resources", nil, &resources); err != nil {
		return err
	}
	for _, app := range []string{"docker", "mysql", "redis", "minio", "nacos", "aifar"} {
		if !resourceAvailable(resources, app) {
			return fmt.Errorf("required E2E resource is missing: %s/backend", app)
		}
	}

	instances, err := r.listInstances(ctx)
	if err != nil {
		return err
	}
	if conflicts := e2eInstanceConflicts(instances, r.cfg.ServerIDs, r.cfg.PrimaryServer); len(conflicts) > 0 {
		sort.Strings(conflicts)
		return fmt.Errorf("target servers already have app instances: %s", strings.Join(conflicts, ", "))
	}
	return nil
}

func (r *e2eRunner) installScenario(ctx context.Context) error {
	specs := e2eBaseInstallSpecs(r.cfg)
	for _, spec := range specs {
		ids, err := r.installAndCheck(ctx, spec)
		if spec.AfterHook != nil {
			spec.AfterHook(r, ids)
		}
		if err != nil {
			return err
		}
		if spec.App == "docker" {
			if err := r.verifyDocker(ctx); err != nil {
				return err
			}
		}
	}
	if r.nacosInstanceID == "" {
		return errors.New("Nacos install did not create an instance for AIFAR dependency")
	}
	aifarSpec := e2eAIFARInstallSpec(r.cfg, r.nacosInstanceID)
	ids, err := r.installAndCheck(ctx, aifarSpec)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return errors.New("AIFAR install did not create an instance")
	}
	return r.waitAIFARRuntimeReady(ctx, ids[0])
}

func e2eBaseInstallSpecs(cfg e2eConfig) []e2eInstallSpec {
	primaryTargets := func() []string { return []string{cfg.PrimaryServer} }
	return []e2eInstallSpec{
		{
			App:     "docker",
			Targets: append([]string(nil), cfg.ServerIDs...),
			Request: map[string]any{"version": "latest", "serverIds": append([]string(nil), cfg.ServerIDs...), "topology": "default", "language": "zh"},
			Check:   true,
		},
		{
			App:     "mysql",
			Targets: primaryTargets(),
			Request: map[string]any{"version": "latest", "serverId": cfg.PrimaryServer, "topology": "standalone", "language": "zh", "rootPassword": cfg.AppPassword},
			Check:   true,
		},
		{
			App:     "redis",
			Targets: primaryTargets(),
			Request: map[string]any{"version": "latest", "serverId": cfg.PrimaryServer, "topology": "standalone", "language": "zh", "password": cfg.AppPassword},
			Check:   true,
		},
		{
			App:     "minio",
			Targets: primaryTargets(),
			Request: map[string]any{"version": "latest", "serverId": cfg.PrimaryServer, "topology": "standalone", "language": "zh", "rootUser": "minioadmin", "rootPassword": cfg.AppPassword},
			Check:   true,
		},
		{
			App:     "nacos",
			Targets: primaryTargets(),
			Request: map[string]any{"version": "latest", "serverId": cfg.PrimaryServer, "topology": "standalone", "language": "zh", "dbSource": "local", "nacosUser": "nacos", "nacosPassword": cfg.AppPassword},
			Check:   true,
			AfterHook: func(r *e2eRunner, ids []string) {
				if len(ids) > 0 {
					r.nacosInstanceID = ids[0]
				}
			},
		},
	}
}

func e2eAIFARInstallSpec(cfg e2eConfig, nacosInstanceID string) e2eInstallSpec {
	return e2eInstallSpec{
		App:     "aifar",
		Targets: []string{cfg.PrimaryServer},
		Request: map[string]any{
			"version":         "runtime-v2",
			"serverId":        cfg.PrimaryServer,
			"topology":        "single",
			"language":        "zh",
			"nacosSource":     "existing",
			"nacosInstanceId": nacosInstanceID,
			"nacosPassword":   cfg.AppPassword,
		},
		Check: true,
	}
}

func (r *e2eRunner) installAndCheck(ctx context.Context, spec e2eInstallSpec) ([]string, error) {
	before, err := r.listInstances(ctx)
	if err != nil {
		return nil, err
	}
	stage := newE2EStage(spec.App, "install")
	var start taskStartResponse
	err = r.client.do(ctx, "install "+spec.App, http.MethodPost, "/api/v2/apps/"+url.PathEscape(spec.App)+"/install", spec.Request, &start)
	if err != nil {
		finishE2EStage(&stage, "failed", err.Error())
		r.report.Stages = append(r.report.Stages, stage)
		return nil, err
	}
	stage.TaskID = start.TaskID
	task, err := r.waitTask(ctx, start.TaskID)
	after, listErr := r.listInstances(ctx)
	ids := createdInstanceIDs(before, after, spec.App, spec.Targets)
	stage.InstanceIDs = ids
	for _, id := range ids {
		if instance, ok := instanceByID(after, id); ok {
			r.created[id] = instance
			r.createdOrder = append(r.createdOrder, id)
		}
	}
	if listErr != nil && err == nil {
		err = listErr
	}
	status := task.Status
	if status == "" && err != nil {
		status = "failed"
	}
	finishE2EStage(&stage, status, task.Error)
	r.report.Stages = append(r.report.Stages, stage)
	if err != nil {
		return ids, fmt.Errorf("%s install task failed: %w", spec.App, err)
	}
	if task.Status != "success" {
		return ids, fmt.Errorf("%s install task status=%s: %s", spec.App, task.Status, task.Error)
	}
	if len(ids) == 0 {
		return ids, fmt.Errorf("%s install task succeeded but created no app instance", spec.App)
	}
	if spec.Check {
		for _, id := range ids {
			if err := r.checkInstance(ctx, spec.App, id); err != nil {
				return ids, err
			}
		}
	}
	return ids, nil
}

func (r *e2eRunner) checkInstance(ctx context.Context, app, id string) error {
	stage := newE2EStage(app, "check")
	stage.InstanceIDs = []string{id}
	var start taskStartResponse
	err := r.client.do(ctx, "check "+app, http.MethodPost, "/api/v2/apps/instances/"+url.PathEscape(id)+"/check", map[string]any{"language": "zh"}, &start)
	if err != nil {
		finishE2EStage(&stage, "failed", err.Error())
		r.report.Stages = append(r.report.Stages, stage)
		return err
	}
	stage.TaskID = start.TaskID
	task, err := r.waitTask(ctx, start.TaskID)
	finishE2EStage(&stage, task.Status, task.Error)
	r.report.Stages = append(r.report.Stages, stage)
	if err != nil {
		return fmt.Errorf("%s check task failed: %w", app, err)
	}
	if task.Status != "success" {
		return fmt.Errorf("%s check task status=%s: %s", app, task.Status, task.Error)
	}
	return nil
}

func (r *e2eRunner) cleanup(ctx context.Context) error {
	if r.cfg.KeepResources {
		for i := range r.report.Stages {
			if r.report.Stages[i].Action == "install" && len(r.report.Stages[i].InstanceIDs) > 0 {
				r.report.Stages[i].CleanupStatus = "kept"
			}
		}
		r.report.Skipped = append(r.report.Skipped, "cleanup skipped because AIFAR_E2E_KEEP_RESOURCES=1")
		return nil
	}
	ids, err := r.cleanupInstanceIDs(ctx)
	if err != nil {
		return err
	}
	var cleanupErrs []error
	for _, id := range ids {
		instance := r.created[id]
		stage := newE2EStage(instance.App, "delete")
		stage.InstanceIDs = []string{id}
		password := r.serverPasswords[instance.ServerID]
		if strings.TrimSpace(password) == "" {
			err := fmt.Errorf("missing server password for cleanup: server=%s instance=%s", instance.ServerID, id)
			finishE2EStage(&stage, "failed", err.Error())
			r.report.Stages = append(r.report.Stages, stage)
			cleanupErrs = append(cleanupErrs, err)
			continue
		}
		var start taskStartResponse
		err := r.client.do(ctx, "delete "+instance.App, http.MethodPost, "/api/v2/apps/instances/"+url.PathEscape(id)+"/delete", map[string]any{
			"language":       "zh",
			"serverPassword": password,
		}, &start)
		if err != nil {
			finishE2EStage(&stage, "failed", err.Error())
			r.report.Stages = append(r.report.Stages, stage)
			cleanupErrs = append(cleanupErrs, err)
			continue
		}
		stage.TaskID = start.TaskID
		task, waitErr := r.waitTask(ctx, start.TaskID)
		finishE2EStage(&stage, task.Status, task.Error)
		if waitErr == nil && task.Status == "success" {
			stage.CleanupStatus = "deleted"
		} else {
			stage.CleanupStatus = "failed"
			if waitErr != nil {
				cleanupErrs = append(cleanupErrs, waitErr)
			} else {
				cleanupErrs = append(cleanupErrs, fmt.Errorf("cleanup %s status=%s: %s", id, task.Status, task.Error))
			}
		}
		r.report.Stages = append(r.report.Stages, stage)
	}
	return errors.Join(cleanupErrs...)
}

func (r *e2eRunner) cleanupInstanceIDs(ctx context.Context) ([]string, error) {
	ids := append([]string(nil), r.createdOrder...)
	if r.cfg.DockerCleanup == "all" {
		instances, err := r.listInstances(ctx)
		if err != nil {
			return nil, err
		}
		for _, instance := range instances {
			if instance.App == "docker" && containsString(r.cfg.ServerIDs, instance.ServerID) && !containsString(ids, instance.ID) {
				r.created[instance.ID] = instance
				ids = append(ids, instance.ID)
			}
		}
	}
	filtered := make([]string, 0, len(ids))
	for _, id := range ids {
		instance := r.created[id]
		if instance.App == "docker" && r.cfg.DockerCleanup == "" {
			for i := range r.report.Stages {
				if containsString(r.report.Stages[i].InstanceIDs, id) {
					r.report.Stages[i].CleanupStatus = "kept-docker"
				}
			}
			continue
		}
		filtered = append(filtered, id)
	}
	for i, j := 0, len(filtered)-1; i < j; i, j = i+1, j-1 {
		filtered[i], filtered[j] = filtered[j], filtered[i]
	}
	return filtered, nil
}

func (r *e2eRunner) verifyDocker(ctx context.Context) error {
	for _, serverID := range r.cfg.ServerIDs {
		var out dockerSummaryAPIResponse
		path := "/api/v2/containers/summary?serverId=" + url.QueryEscape(serverID)
		err := r.client.do(ctx, "docker summary "+serverID, http.MethodGet, path, nil, &out)
		check := e2eRemoteCheck{Name: "docker.summary", ServerID: serverID, Status: "passed", Details: map[string]any{}}
		if err != nil {
			check.Status = "failed"
			check.Message = cleanErrText(err.Error())
			r.report.RemoteChecks = append(r.report.RemoteChecks, check)
			return err
		}
		if !out.Available {
			check.Status = "failed"
			check.Message = cleanErrText(out.Error)
			r.report.RemoteChecks = append(r.report.RemoteChecks, check)
			return fmt.Errorf("docker summary unavailable for server %s: %s", serverID, out.Error)
		}
		for key, value := range out.Summary {
			check.Details[key] = value
		}
		r.report.RemoteChecks = append(r.report.RemoteChecks, check)
	}
	return nil
}

func (r *e2eRunner) waitAIFARRuntimeReady(ctx context.Context, instanceID string) error {
	deadline := time.Now().Add(r.cfg.RuntimeTimeout)
	var lastErr error
	for {
		ready, err := r.checkAIFARRuntime(ctx, instanceID)
		if ready && err == nil {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return lastErr
			}
			return errors.New("AIFAR runtime did not become ready before timeout")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}

func (r *e2eRunner) checkAIFARRuntime(ctx context.Context, instanceID string) (bool, error) {
	var out aifarRuntimeAPIResponse
	path := "/api/v2/containers/aifar/runtime?includePods=1&includeStats=0&serverId=" + url.QueryEscape(r.cfg.PrimaryServer)
	err := r.client.do(ctx, "aifar runtime", http.MethodGet, path, nil, &out)
	check := e2eRemoteCheck{Name: "aifar.runtime", ServerID: r.cfg.PrimaryServer, Status: "passed", Details: map[string]any{}}
	if err != nil {
		check.Status = "failed"
		check.Message = cleanErrText(err.Error())
		r.report.RemoteChecks = append(r.report.RemoteChecks, check)
		return false, err
	}
	desired, ready, active := 0, 0, 0
	for _, service := range out.Services {
		if service.InstanceID != "" && service.InstanceID != instanceID {
			continue
		}
		desired += service.DesiredReplicas
		ready += service.ReadyReplicas
		active += service.ActiveEndpoints
		if strings.EqualFold(service.Status, "failed") || strings.EqualFold(service.Status, "degraded") {
			check.Status = "failed"
			check.Message = fmt.Sprintf("service %s status=%s: %s%s", service.ServiceName, service.Status, service.FailureReason, service.LastError)
		}
	}
	check.Details["runtimeStatus"] = out.RuntimeStatus
	check.Details["desiredReplicas"] = desired
	check.Details["readyReplicas"] = ready
	check.Details["activeEndpoints"] = active
	if out.RuntimeStatus == "failed" || out.RuntimeStatus == "degraded" || desired == 0 || ready != desired || active <= 0 {
		check.Status = "failed"
		if check.Message == "" {
			check.Message = fmt.Sprintf("runtimeStatus=%s desired=%d ready=%d activeEndpoints=%d", out.RuntimeStatus, desired, ready, active)
		}
		r.report.RemoteChecks = append(r.report.RemoteChecks, check)
		return false, errors.New(check.Message)
	}
	r.report.RemoteChecks = append(r.report.RemoteChecks, check)
	return true, nil
}

func (r *e2eRunner) waitTask(ctx context.Context, taskID string) (store.Task, error) {
	if strings.TrimSpace(taskID) == "" {
		return store.Task{}, errors.New("task id is required")
	}
	deadline := time.Now().Add(r.cfg.TaskTimeout)
	for {
		var detail taskDetailResponse
		err := r.client.do(ctx, "task "+taskID, http.MethodGet, "/api/v2/tasks/"+url.PathEscape(taskID), nil, &detail)
		if err != nil {
			return detail.Task, err
		}
		switch detail.Task.Status {
		case "success":
			return detail.Task, nil
		case "failed", "cancelled", "timeout":
			return detail.Task, fmt.Errorf("task %s status=%s: %s", taskID, detail.Task.Status, detail.Task.Error)
		}
		if time.Now().After(deadline) {
			return detail.Task, fmt.Errorf("task %s timed out waiting for completion; last status=%s", taskID, detail.Task.Status)
		}
		select {
		case <-ctx.Done():
			return detail.Task, ctx.Err()
		case <-time.After(r.cfg.PollInterval):
		}
	}
}

func (r *e2eRunner) listInstances(ctx context.Context) ([]store.AppInstance, error) {
	var instances []store.AppInstance
	if err := r.client.do(ctx, "list app instances", http.MethodGet, "/api/v2/apps/instances", nil, &instances); err != nil {
		return nil, err
	}
	return instances, nil
}

func (c *e2eHTTPClient) do(ctx context.Context, name, method, requestPath string, body any, out any) error {
	start := time.Now()
	check := e2eAPICheckReport{Name: name, Method: method, Path: requestPath, Status: "passed"}
	defer func() {
		check.DurationMS = time.Since(start).Milliseconds()
		*c.checks = append(*c.checks, check)
	}()

	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			check.Status = "failed"
			check.Message = "marshal request failed"
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+requestPath, reader)
	if err != nil {
		check.Status = "failed"
		check.Message = cleanErrText(err.Error())
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-AIFAR-Language", "zh")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		check.Status = "failed"
		check.Message = cleanErrText(err.Error())
		return err
	}
	defer resp.Body.Close()
	check.HTTPStatus = resp.StatusCode
	data, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		check.Status = "failed"
		check.Message = cleanErrText(readErr.Error())
		return readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := apiErrorMessage(data)
		check.Status = "failed"
		check.Message = message
		return fmt.Errorf("%s %s returned %d: %s", method, requestPath, resp.StatusCode, message)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			check.Status = "failed"
			check.Message = cleanErrText(err.Error())
			return err
		}
	}
	return nil
}

func validateE2EConfig(cfg e2eConfig) []string {
	var problems []string
	if !cfg.AllowMutation {
		problems = append(problems, "refused: set AIFAR_E2E_ALLOW_MUTATION=1 to allow mutating E2E")
	}
	if len(cfg.ServerIDs) == 0 {
		problems = append(problems, "refused: set AIFAR_E2E_SERVER_IDS to a comma-separated test server whitelist")
	}
	if strings.TrimSpace(cfg.Username) == "" {
		problems = append(problems, "refused: set AIFAR_E2E_USERNAME")
	}
	if cfg.Password == "" {
		problems = append(problems, "refused: set AIFAR_E2E_PASSWORD")
	}
	if cfg.AppPassword == "" {
		problems = append(problems, "refused: set AIFAR_E2E_APP_PASSWORD")
	}
	if cfg.DockerCleanup != "" && cfg.DockerCleanup != "created" && cfg.DockerCleanup != "all" {
		problems = append(problems, "refused: AIFAR_E2E_DOCKER_CLEANUP must be empty, created, or all")
	}
	if cfg.TaskTimeout <= 0 {
		problems = append(problems, "refused: task timeout must be positive")
	}
	return problems
}

func e2eInstanceConflicts(instances []store.AppInstance, allServerIDs []string, primary string) []string {
	targets := map[string]map[string]bool{
		"docker": setFromStrings(allServerIDs),
		"mysql":  map[string]bool{primary: true},
		"redis":  map[string]bool{primary: true},
		"minio":  map[string]bool{primary: true},
		"nacos":  map[string]bool{primary: true},
		"aifar":  map[string]bool{primary: true},
	}
	var conflicts []string
	for _, instance := range instances {
		if targetSet, ok := targets[instance.App]; ok && targetSet[instance.ServerID] {
			conflicts = append(conflicts, instance.App+":"+instance.ID+"@"+instance.ServerID)
		}
	}
	return conflicts
}

func resourceAvailable(resources []store.Resource, app string) bool {
	for _, resource := range resources {
		if resource.App == app && resource.Part == "backend" {
			return true
		}
	}
	return false
}

func createdInstanceIDs(before, after []store.AppInstance, app string, targets []string) []string {
	beforeIDs := map[string]bool{}
	for _, instance := range before {
		beforeIDs[instance.ID] = true
	}
	targetSet := setFromStrings(targets)
	var ids []string
	for _, instance := range after {
		if beforeIDs[instance.ID] || instance.App != app || !targetSet[instance.ServerID] {
			continue
		}
		ids = append(ids, instance.ID)
	}
	sort.Strings(ids)
	return ids
}

func instanceByID(instances []store.AppInstance, id string) (store.AppInstance, bool) {
	for _, instance := range instances {
		if instance.ID == id {
			return instance, true
		}
	}
	return store.AppInstance{}, false
}

func newE2EStage(app, action string) e2eStageReport {
	return e2eStageReport{App: app, Action: action, Status: "running", StartedAt: time.Now()}
}

func finishE2EStage(stage *e2eStageReport, status, message string) {
	if strings.TrimSpace(status) == "" {
		status = "unknown"
	}
	stage.Status = status
	stage.Message = cleanErrText(message)
	stage.FinishedAt = time.Now()
	stage.DurationMS = stage.FinishedAt.Sub(stage.StartedAt).Milliseconds()
}

func apiErrorMessage(data []byte) string {
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &body) == nil && body.Error.Message != "" {
		if body.Error.Code != "" {
			return body.Error.Code + ": " + cleanErrText(body.Error.Message)
		}
		return cleanErrText(body.Error.Message)
	}
	return cleanErrText(string(data))
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func normalizeDockerCleanup(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func setFromStrings(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
