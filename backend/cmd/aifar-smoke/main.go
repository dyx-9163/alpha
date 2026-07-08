package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/config"
	"aifar-deployment/backend/internal/servers"
	"aifar-deployment/backend/internal/store"
)

type report struct {
	Command          string               `json:"command"`
	StartedAt        time.Time            `json:"startedAt"`
	FinishedAt       time.Time            `json:"finishedAt"`
	DatabasePath     string               `json:"databasePath"`
	OutputPath       string               `json:"outputPath,omitempty"`
	Summary          summary              `json:"summary"`
	Resources        []string             `json:"resources"`
	Tasks            taskSummary          `json:"tasks"`
	RecentProblems   []problemTask        `json:"recentProblems"`
	Collectors       []collectorReport    `json:"collectors"`
	ProblemSnapshots []snapshotReport     `json:"problemSnapshots"`
	OpenAlerts       []alertReport        `json:"openAlerts"`
	Servers          []serverReport       `json:"servers"`
	AppInstances     []appInstanceReport  `json:"appInstances"`
	Guard            *mutatingGuardReport `json:"guard,omitempty"`
	Stages           []e2eStageReport     `json:"stages,omitempty"`
	APIChecks        []e2eAPICheckReport  `json:"apiChecks,omitempty"`
	RemoteChecks     []e2eRemoteCheck     `json:"remoteChecks,omitempty"`
	Passed           bool                 `json:"passed"`
	Failures         []string             `json:"failures"`
	Skipped          []string             `json:"skipped"`
}

type summary struct {
	Servers         int `json:"servers"`
	Resources       int `json:"resources"`
	AppInstances    int `json:"appInstances"`
	Tasks           int `json:"tasks"`
	CollectorRuns   int `json:"collectorRuns"`
	StatusSnapshots int `json:"statusSnapshots"`
	OpenAlerts      int `json:"openAlerts"`
}

type taskSummary struct {
	ByStatus map[string]int `json:"byStatus"`
}

type problemTask struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Target    string    `json:"target"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	Error     string    `json:"error"`
}

type collectorReport struct {
	Name       string    `json:"name"`
	Target     string    `json:"target"`
	Status     string    `json:"status"`
	LastError  string    `json:"lastError,omitempty"`
	DurationMS int64     `json:"durationMs"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type snapshotReport struct {
	Scope       string    `json:"scope"`
	ResourceID  string    `json:"resourceId"`
	ServerID    string    `json:"serverId,omitempty"`
	Status      string    `json:"status"`
	Version     int64     `json:"version"`
	CollectedAt time.Time `json:"collectedAt"`
	LastError   string    `json:"lastError,omitempty"`
	Payload     string    `json:"payload,omitempty"`
}

type alertReport struct {
	Severity   string    `json:"severity"`
	Scope      string    `json:"scope"`
	ResourceID string    `json:"resourceId"`
	Title      string    `json:"title"`
	Message    string    `json:"message,omitempty"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

type serverReport struct {
	ID               string           `json:"id"`
	Name             string           `json:"name"`
	Host             string           `json:"host"`
	Port             int              `json:"port"`
	Username         string           `json:"username"`
	AuthType         string           `json:"authType"`
	SavedStatus      string           `json:"savedStatus"`
	DockerHost       string           `json:"dockerHost"`
	DeployDir        string           `json:"deployDir"`
	Credential       checkResult      `json:"credential"`
	SSH              checkResult      `json:"ssh"`
	Telemetry        telemetryReport  `json:"telemetry"`
	DockerSummary    dockerReport     `json:"dockerSummary"`
	DockerContainers dockerContainers `json:"dockerContainers"`
}

type checkResult struct {
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

type telemetryReport struct {
	checkResult
	CPU      string    `json:"cpu,omitempty"`
	Memory   string    `json:"memory,omitempty"`
	Disk     string    `json:"disk,omitempty"`
	DiskPath string    `json:"diskPath,omitempty"`
	Load     []float64 `json:"load,omitempty"`
}

type dockerReport struct {
	checkResult
	Version    string `json:"version,omitempty"`
	Endpoint   string `json:"endpoint,omitempty"`
	Containers int    `json:"containers,omitempty"`
	Running    int    `json:"running,omitempty"`
	Images     int    `json:"images,omitempty"`
	Networks   int    `json:"networks,omitempty"`
	Volumes    int    `json:"volumes,omitempty"`
}

type dockerContainers struct {
	checkResult
	Total              int `json:"total,omitempty"`
	AIFARLabeledOrName int `json:"aifarLabeledOrNamed,omitempty"`
}

type appInstanceReport struct {
	ID       string `json:"id"`
	App      string `json:"app"`
	Version  string `json:"version"`
	ServerID string `json:"serverId"`
	Status   string `json:"status"`
	Topology string `json:"topology"`
	Check    string `json:"check"`
	Message  string `json:"message,omitempty"`
}

type mutatingGuardReport struct {
	AllowMutation bool     `json:"allowMutation"`
	ServerIDs     []string `json:"serverIds"`
	Message       string   `json:"message"`
}

func main() {
	command := "readonly"
	args := os.Args[1:]
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}

	var code int
	switch command {
	case "readonly":
		code = runReadonly(args)
	case "nacos-diagnose":
		code = runNacosDiagnose(args)
	case "e2e-mutating":
		code = runE2EMutating(args)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", command)
		printUsage()
		code = 2
	}
	os.Exit(code)
}

func runReadonly(args []string) int {
	cfg := config.Load()
	flags := flag.NewFlagSet("readonly", flag.ContinueOnError)
	databasePath := flags.String("database", cfg.DatabasePath, "SQLite database path")
	outputRoot := flags.String("output-dir", defaultOutputRoot(), "directory for JSON smoke reports")
	timeout := flags.Duration("timeout", 25*time.Second, "per-check timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	rep := newReport("readonly", *databasePath)
	db, err := store.OpenReadOnlyWithSecret(*databasePath, cfg.CredentialSecret)
	if err != nil {
		rep.Failures = append(rep.Failures, "open read-only database: "+cleanErrText(err.Error()))
		rep.FinishedAt = time.Now()
		_ = writeReport(*outputRoot, &rep)
		printReport(rep)
		return 1
	}
	defer db.Close()

	if err := collectInventory(db, &rep); err != nil {
		rep.Failures = append(rep.Failures, cleanErrText(err.Error()))
		rep.FinishedAt = time.Now()
		_ = writeReport(*outputRoot, &rep)
		printReport(rep)
		return 1
	}

	serverService := servers.NewServiceWithRemote(db, nil, adapter.SSHRemote{}, cfg.DefaultDeployDir)
	publicServers, _ := db.ListServers()
	if len(publicServers) == 0 {
		rep.Skipped = append(rep.Skipped, "server checks skipped: no configured servers")
	}
	for _, item := range publicServers {
		server, err := db.GetServer(item.ID, true)
		if err != nil {
			rep.Servers = append(rep.Servers, serverReport{
				ID:   item.ID,
				Name: item.Name,
				SSH:  failedCheck("load saved server secret: "+err.Error(), "verify the local database and credential secret"),
			})
			rep.Failures = append(rep.Failures, fmt.Sprintf("server %s: failed to load saved secret", item.ID))
			continue
		}
		serverRep := checkServer(serverService, server, *timeout)
		rep.Servers = append(rep.Servers, serverRep)
		appendServerFailures(&rep, serverRep)
	}

	if rep.Summary.AppInstances == 0 {
		rep.Skipped = append(rep.Skipped, "app instance checks skipped: no app_instances rows")
	}

	rep.Passed = len(rep.Failures) == 0
	rep.FinishedAt = time.Now()
	if err := writeReport(*outputRoot, &rep); err != nil {
		fmt.Fprintf(os.Stderr, "write smoke report failed: %v\n", err)
		if rep.Passed {
			rep.Passed = false
			rep.Failures = append(rep.Failures, "write JSON report failed")
		}
	}
	printReport(rep)
	if rep.Passed {
		return 0
	}
	return 1
}

func runE2EMutating(args []string) int {
	return runE2EMutatingScenario(args)
}

func collectInventory(db *store.Store, rep *report) error {
	serversList, err := db.ListServers()
	if err != nil {
		return fmt.Errorf("list servers: %w", err)
	}
	resources, err := db.ListResources()
	if err != nil {
		return fmt.Errorf("list resources: %w", err)
	}
	instances, err := db.ListAppInstances()
	if err != nil {
		return fmt.Errorf("list app instances: %w", err)
	}
	tasks, err := db.ListTasks()
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}
	collectors, err := db.ListCollectorRuns()
	if err != nil {
		return fmt.Errorf("list collector runs: %w", err)
	}
	snapshots, err := db.ListStatusSnapshots("", "")
	if err != nil {
		return fmt.Errorf("list status snapshots: %w", err)
	}
	alerts, err := db.ListAlerts(store.AlertQuery{Status: "open"})
	if err != nil {
		return fmt.Errorf("list open alerts: %w", err)
	}

	rep.Summary = summary{
		Servers:         len(serversList),
		Resources:       len(resources),
		AppInstances:    len(instances),
		Tasks:           len(tasks),
		CollectorRuns:   len(collectors),
		StatusSnapshots: len(snapshots),
		OpenAlerts:      len(alerts),
	}
	rep.Resources = resourceSummary(resources)
	rep.Tasks = taskSummary{ByStatus: countTasks(tasks)}
	rep.RecentProblems = recentProblemTasks(tasks, 10)
	rep.Collectors = collectorReports(collectors)
	rep.ProblemSnapshots = problemSnapshots(snapshots)
	rep.OpenAlerts = alertReports(alerts, 20)
	rep.AppInstances = appInstanceReports(instances)
	return nil
}

func checkServer(serverService servers.Service, server store.Server, timeout time.Duration) serverReport {
	rep := serverReport{
		ID:          server.ID,
		Name:        server.Name,
		Host:        server.Host,
		Port:        server.Port,
		Username:    server.Username,
		AuthType:    server.AuthType,
		SavedStatus: server.Status,
		DockerHost:  dockerHostLabel(server.DockerHost),
		DeployDir:   server.DeployDir,
	}

	if strings.TrimSpace(server.Password) == "" && strings.TrimSpace(server.PrivateKey) == "" {
		rep.Credential = failedCheck("missing saved password/private key", "save a password or private key for this server")
		return rep
	}
	rep.Credential = passedCheck("saved secret present")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	err := adapter.ProbeSSH(ctx, server)
	cancel()
	if err != nil {
		rep.SSH = failedCheck(err.Error(), suggestionForError(err))
		return rep
	}
	rep.SSH = passedCheck("ssh login succeeded")

	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	telemetry, err := serverService.Telemetry(ctx, server.ID)
	cancel()
	if err != nil {
		rep.Telemetry = telemetryReport{checkResult: failedCheck(err.Error(), suggestionForError(err))}
	} else {
		rep.Telemetry = telemetryReport{
			checkResult: passedCheck("telemetry collected"),
			CPU:         telemetry.CPUText,
			Memory:      telemetry.MemoryText,
			Disk:        telemetry.DiskText,
			DiskPath:    telemetry.DiskPath,
			Load:        telemetry.Load,
		}
	}

	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	summary, err := adapter.DockerSummaryForServer(ctx, server)
	cancel()
	if err != nil {
		rep.DockerSummary = dockerReport{checkResult: failedCheck(err.Error(), suggestionForError(err))}
		return rep
	}
	rep.DockerSummary = dockerReport{
		checkResult: passedCheck("docker summary collected"),
		Version:     summary.Version,
		Endpoint:    summary.Endpoint,
		Containers:  summary.Containers,
		Running:     summary.Running,
		Images:      summary.Images,
		Networks:    summary.Networks,
		Volumes:     summary.Volumes,
	}

	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	containers, err := adapter.DockerContainersForServer(ctx, server)
	cancel()
	if err != nil {
		rep.DockerContainers = dockerContainers{checkResult: failedCheck(err.Error(), suggestionForError(err))}
		return rep
	}
	aifarCount := 0
	for _, container := range containers {
		if isAIFARContainer(container) {
			aifarCount++
		}
	}
	rep.DockerContainers = dockerContainers{
		checkResult:        passedCheck("docker containers listed"),
		Total:              len(containers),
		AIFARLabeledOrName: aifarCount,
	}
	return rep
}

func appendServerFailures(rep *report, server serverReport) {
	checks := []struct {
		name   string
		status string
	}{
		{name: "credential", status: server.Credential.Status},
		{name: "ssh", status: server.SSH.Status},
		{name: "telemetry", status: server.Telemetry.Status},
		{name: "docker_summary", status: server.DockerSummary.Status},
		{name: "docker_containers", status: server.DockerContainers.Status},
	}
	for _, check := range checks {
		if check.status == "failed" {
			rep.Failures = append(rep.Failures, fmt.Sprintf("server %s (%s): %s failed", server.ID, server.Host, check.name))
		}
	}
}

func resourceSummary(resources []store.Resource) []string {
	counts := map[string]int{}
	for _, resource := range resources {
		key := resource.App
		if resource.Part != "" {
			key += "/" + resource.Part
		}
		key += "@" + resource.Version
		counts[key]++
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func countTasks(tasks []store.Task) map[string]int {
	counts := map[string]int{}
	for _, task := range tasks {
		counts[task.Status]++
	}
	return counts
}

func recentProblemTasks(tasks []store.Task, limit int) []problemTask {
	out := []problemTask{}
	for _, task := range tasks {
		if task.Status != "failed" && task.Status != "running" && task.Status != "pending" {
			continue
		}
		out = append(out, problemTask{
			ID:        task.ID,
			Type:      task.Type,
			Target:    task.Target,
			Status:    task.Status,
			CreatedAt: task.CreatedAt,
			Error:     cleanErrText(task.Error),
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func collectorReports(runs []store.CollectorRun) []collectorReport {
	out := make([]collectorReport, 0, len(runs))
	for _, run := range runs {
		out = append(out, collectorReport{
			Name:       run.Name,
			Target:     run.Target,
			Status:     run.Status,
			LastError:  cleanErrText(run.LastError),
			DurationMS: run.DurationMS,
			UpdatedAt:  run.UpdatedAt,
		})
	}
	return out
}

func problemSnapshots(snapshots []store.StatusSnapshot) []snapshotReport {
	out := []snapshotReport{}
	for _, snapshot := range snapshots {
		status := strings.ToLower(strings.TrimSpace(snapshot.Status))
		if status == "" || status == "ok" || status == "success" || status == "available" || status == "healthy" || status == "running" {
			continue
		}
		out = append(out, snapshotReport{
			Scope:       snapshot.Scope,
			ResourceID:  snapshot.ResourceID,
			ServerID:    snapshot.ServerID,
			Status:      snapshot.Status,
			Version:     snapshot.Version,
			CollectedAt: snapshot.CollectedAt,
			LastError:   cleanErrText(snapshot.LastError),
			Payload:     cleanPayload(snapshot.Payload),
		})
		if len(out) >= 20 {
			break
		}
	}
	return out
}

func alertReports(alerts []store.Alert, limit int) []alertReport {
	out := []alertReport{}
	for _, alert := range alerts {
		out = append(out, alertReport{
			Severity:   alert.Severity,
			Scope:      alert.Scope,
			ResourceID: alert.ResourceID,
			Title:      alert.Title,
			Message:    cleanErrText(alert.Message),
			LastSeenAt: alert.LastSeenAt,
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func appInstanceReports(instances []store.AppInstance) []appInstanceReport {
	out := make([]appInstanceReport, 0, len(instances))
	for _, instance := range instances {
		out = append(out, appInstanceReport{
			ID:       instance.ID,
			App:      instance.App,
			Version:  instance.Version,
			ServerID: instance.ServerID,
			Status:   instance.Status,
			Topology: instance.Topology,
			Check:    "skipped",
			Message:  "read-only smoke does not call app Check modules because they may update stored instance status",
		})
	}
	return out
}

func writeReport(outputRoot string, rep *report) error {
	if strings.TrimSpace(outputRoot) == "" {
		return errors.New("output directory is required")
	}
	runDir := filepath.Join(outputRoot, rep.StartedAt.Format("20060102-150405"))
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(runDir, rep.Command+".json")
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return err
	}
	rep.OutputPath = path
	data, err = json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func printReport(rep report) {
	fmt.Printf("AIFAR smoke %s started=%s finished=%s\n", rep.Command, rep.StartedAt.Format(time.RFC3339), rep.FinishedAt.Format(time.RFC3339))
	fmt.Printf("database=%s\n", rep.DatabasePath)
	if rep.OutputPath != "" {
		fmt.Printf("json_report=%s\n", rep.OutputPath)
	}
	fmt.Printf("inventory: servers=%d resources=%d app_instances=%d tasks=%d collector_runs=%d status_snapshots=%d open_alerts=%d\n",
		rep.Summary.Servers, rep.Summary.Resources, rep.Summary.AppInstances, rep.Summary.Tasks, rep.Summary.CollectorRuns, rep.Summary.StatusSnapshots, rep.Summary.OpenAlerts)
	if len(rep.Resources) > 0 {
		fmt.Printf("resources: %s\n", strings.Join(rep.Resources, " "))
	} else {
		fmt.Println("resources: none")
	}
	fmt.Printf("tasks: %s\n", formatCounts(rep.Tasks.ByStatus))
	printProblems(rep)
	for i, server := range rep.Servers {
		printServer(i+1, len(rep.Servers), server)
	}
	if rep.Guard != nil {
		fmt.Printf("guard: allow_mutation=%v server_ids=%s message=%s\n", rep.Guard.AllowMutation, strings.Join(rep.Guard.ServerIDs, ","), rep.Guard.Message)
	}
	if len(rep.Skipped) > 0 {
		fmt.Println("skipped:")
		for _, item := range rep.Skipped {
			fmt.Printf("  - %s\n", item)
		}
	}
	if len(rep.Failures) > 0 {
		fmt.Println("failures:")
		for _, item := range rep.Failures {
			fmt.Printf("  - %s\n", item)
		}
	}
	if rep.Passed {
		fmt.Println("result=PASS")
	} else {
		fmt.Println("result=FAIL")
	}
}

func printProblems(rep report) {
	if len(rep.RecentProblems) > 0 {
		fmt.Println("active_or_failed_tasks:")
		for _, task := range rep.RecentProblems {
			fmt.Printf("  id=%s type=%s target=%s status=%s created_at=%s error=%s\n",
				task.ID, task.Type, task.Target, task.Status, task.CreatedAt.Format(time.RFC3339), task.Error)
		}
	} else {
		fmt.Println("active_or_failed_tasks: none")
	}
	if len(rep.Collectors) > 0 {
		fmt.Println("collectors:")
		for _, run := range rep.Collectors {
			fmt.Printf("  %s target=%s status=%s duration_ms=%d updated_at=%s last_error=%s\n",
				clean(run.Name), clean(run.Target), clean(run.Status), run.DurationMS, formatTime(run.UpdatedAt), emptyDash(run.LastError))
		}
	}
	if len(rep.ProblemSnapshots) > 0 {
		fmt.Println("problem_snapshots:")
		for _, snapshot := range rep.ProblemSnapshots {
			fmt.Printf("  scope=%s resource=%s server=%s status=%s version=%d collected_at=%s last_error=%s payload=%s\n",
				clean(snapshot.Scope), clean(snapshot.ResourceID), clean(snapshot.ServerID), clean(snapshot.Status),
				snapshot.Version, formatTime(snapshot.CollectedAt), emptyDash(snapshot.LastError), emptyDash(snapshot.Payload))
		}
	} else {
		fmt.Println("problem_snapshots: none")
	}
	if len(rep.OpenAlerts) > 0 {
		fmt.Println("open_alerts:")
		for _, alert := range rep.OpenAlerts {
			fmt.Printf("  severity=%s scope=%s resource=%s title=%s last_seen=%s\n",
				clean(alert.Severity), clean(alert.Scope), clean(alert.ResourceID), clean(alert.Title), formatTime(alert.LastSeenAt))
		}
	} else {
		fmt.Println("open_alerts: none")
	}
}

func printServer(index, total int, server serverReport) {
	fmt.Printf("\n[%d/%d] server=%s id=%s host=%s:%d user=%s auth=%s saved_status=%s docker_host=%s deploy_dir=%s\n",
		index, total, clean(server.Name), server.ID, clean(server.Host), server.Port, clean(server.Username), clean(server.AuthType),
		clean(server.SavedStatus), clean(server.DockerHost), clean(server.DeployDir))
	printCheck("credential", server.Credential)
	printCheck("ssh", server.SSH)
	printTelemetry(server.Telemetry)
	printDockerSummary(server.DockerSummary)
	printDockerContainers(server.DockerContainers)
}

func printCheck(name string, result checkResult) {
	if result.Status == "" {
		return
	}
	fmt.Printf("  %s=%s", name, strings.ToUpper(result.Status))
	if result.Message != "" {
		fmt.Printf(" message=%s", result.Message)
	}
	if result.Suggestion != "" {
		fmt.Printf(" suggestion=%s", result.Suggestion)
	}
	fmt.Println()
}

func printTelemetry(result telemetryReport) {
	if result.Status == "" {
		return
	}
	if result.Status != "passed" {
		printCheck("telemetry", result.checkResult)
		return
	}
	fmt.Printf("  telemetry=PASS cpu=%s memory=%s disk=%s disk_path=%s load=%s\n",
		result.CPU, result.Memory, result.Disk, clean(result.DiskPath), formatLoad(result.Load))
}

func printDockerSummary(result dockerReport) {
	if result.Status == "" {
		return
	}
	if result.Status != "passed" {
		printCheck("docker_summary", result.checkResult)
		return
	}
	fmt.Printf("  docker_summary=PASS version=%s containers=%d running=%d images=%d networks=%d volumes=%d endpoint=%s\n",
		clean(result.Version), result.Containers, result.Running, result.Images, result.Networks, result.Volumes, clean(result.Endpoint))
}

func printDockerContainers(result dockerContainers) {
	if result.Status == "" {
		return
	}
	if result.Status != "passed" {
		printCheck("docker_containers", result.checkResult)
		return
	}
	fmt.Printf("  docker_containers=PASS total=%d aifar_labeled_or_named=%d\n", result.Total, result.AIFARLabeledOrName)
}

func newReport(command, databasePath string) report {
	return report{
		Command:      command,
		StartedAt:    time.Now(),
		DatabasePath: databasePath,
		Tasks:        taskSummary{ByStatus: map[string]int{}},
	}
}

func passedCheck(message string) checkResult {
	return checkResult{Status: "passed", Message: message}
}

func failedCheck(message, suggestion string) checkResult {
	return checkResult{Status: "failed", Message: cleanErrText(message), Suggestion: suggestion}
}

func suggestionForError(err error) string {
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "docker: 未找到命令"), strings.Contains(text, "docker: command not found"), strings.Contains(text, "docker: not found"):
		return "install Docker on the server, or run the Docker app install workflow first"
	case strings.Contains(text, "cannot connect to the docker daemon"), strings.Contains(text, "is the docker daemon running"):
		return "start and enable the Docker daemon, then rerun the smoke test"
	case strings.Contains(text, "handshake failed"), strings.Contains(text, "eof"):
		return "verify sshd, credentials, MaxStartups, and network stability"
	case strings.Contains(text, "connection refused"), strings.Contains(text, "no route to host"), strings.Contains(text, "timed out"):
		return "verify host reachability, firewall rules, and SSH port"
	default:
		return "inspect the server runtime and rerun the smoke test"
	}
}

func isAIFARContainer(container adapter.DockerContainer) bool {
	name := strings.ToLower(strings.TrimPrefix(container.Name, "/"))
	if strings.HasPrefix(name, "aifar") || strings.Contains(name, "-aifar-") {
		return true
	}
	for key, value := range container.Labels {
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.ToLower(strings.TrimSpace(value))
		if strings.HasPrefix(key, "aifar.") || strings.Contains(value, "aifar") {
			return true
		}
	}
	return false
}

func dockerHostLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "(ssh)"
	}
	return value
}

func defaultOutputRoot() string {
	if value := strings.TrimSpace(os.Getenv("AIFAR_SMOKE_OUTPUT_DIR")); value != "" {
		return value
	}
	return filepath.Join("outputs", "test-runs")
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func formatCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, " ")
}

func formatLoad(values []float64) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%.2f", value))
	}
	return strings.Join(parts, ",")
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Format(time.RFC3339)
}

func cleanErrText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	if len(value) > 300 {
		value = value[:300] + "..."
	}
	return value
}

func cleanPayload(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	if len(value) > 600 {
		value = value[:600] + "..."
	}
	return value
}

func clean(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func emptyDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func printUsage() {
	fmt.Println(`Usage:
  aifar-smoke readonly [--database path] [--output-dir dir] [--timeout 25s]
  aifar-smoke nacos-diagnose [--database path] [--output-dir dir] [--server-ids id1,id2] [--timeout 45s]
  aifar-smoke e2e-mutating [--database path] [--output-dir dir] [--server-ids id1,id2]

The e2e-mutating command refuses to run unless AIFAR_E2E_ALLOW_MUTATION=1
and AIFAR_E2E_SERVER_IDS or --server-ids is provided.`)
}
