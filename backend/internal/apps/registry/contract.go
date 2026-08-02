package registry

import (
	"context"
	"io"
	"strings"
	"time"

	"aifar-deployment/backend/internal/store"
)

type Logger interface {
	Info(format string, args ...any)
	Error(format string, args ...any)
}

type InstallStepPlan struct {
	Target string
	Name   string
	Title  string
	Order  int
}

type PreflightResult struct {
	Warnings []string
}

type TargetMode string

const (
	TargetModeSingle   TargetMode = "single"
	TargetModeMultiple TargetMode = "multiple"
)

type Topology struct {
	Name       string     `json:"name"`
	Label      string     `json:"label"`
	TargetMode TargetMode `json:"targetMode"`
	MinTargets int        `json:"minTargets"`
	Default    bool       `json:"default"`
}

type Manifest struct {
	Name                   string
	Title                  string
	Icon                   string
	Category               string
	CategoryLabel          string
	SourceLabel            string
	FallbackVersion        string
	Description            string
	InstallName            string
	ResourceApp            string
	ResourceVersionPattern string
	RequiresServer         bool
	SupportsMultiTarget    bool
	BackendReady           bool
	RequiredResourceParts  []string
	Capabilities           []string
	Topologies             []Topology
}

// InstallModuleDefinition describes one installable module discovered from an
// application's offline resource bundle. The frontend consumes this contract
// instead of maintaining a second hard-coded module list.
type InstallModuleDefinition struct {
	Name               string   `json:"name"`
	DisplayName        string   `json:"displayName"`
	Kind               string   `json:"kind"`
	ApplicationName    string   `json:"applicationName,omitempty"`
	Port               int      `json:"port"`
	Required           bool     `json:"required"`
	Role               string   `json:"role,omitempty"`
	ArtifactExtensions []string `json:"artifactExtensions,omitempty"`
	HealthPath         string   `json:"healthPath,omitempty"`
	AffinityPolicy     string   `json:"affinityPolicy,omitempty"`
}

// InstallModuleProvider is optional. Modules implement it when their offline
// bundle defines install choices dynamically.
type InstallModuleProvider interface {
	InstallModules(resources []store.Resource, version, language string) ([]InstallModuleDefinition, error)
}

func (m Manifest) SelectedTopology(name string) (Topology, bool) {
	name = strings.TrimSpace(strings.ToLower(name))
	if len(m.Topologies) == 0 {
		return Topology{}, false
	}
	if name != "" {
		for _, topology := range m.Topologies {
			if strings.EqualFold(topology.Name, name) {
				return topology, true
			}
		}
	}
	for _, topology := range m.Topologies {
		if topology.Default {
			return topology, true
		}
	}
	return m.Topologies[0], true
}

func (m Manifest) AllowsMultiTargetFor(topology string) bool {
	selected, ok := m.SelectedTopology(topology)
	if !ok {
		return m.SupportsMultiTarget
	}
	return selected.TargetMode == TargetModeMultiple
}

type InstallRequest struct {
	App             string
	Version         string
	Topology        string
	Language        string
	ServerID        string
	ServerIDs       []string
	Actor           string
	DefaultPassword string
	Parameters      map[string]any
}

func (r InstallRequest) TargetServerIDs() []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	add(r.ServerID)
	for _, id := range r.ServerIDs {
		add(id)
	}
	return out
}

type DeleteRequest struct {
	Instance   store.AppInstance
	Server     store.Server
	Language   string
	Actor      string
	Parameters map[string]any
	Batch      *DeleteBatchScope
}

// DeleteBatchScope is created by the HTTP boundary only after an application
// module has successfully preflighted the complete batch. It is intentionally
// not decoded from client JSON.
type DeleteBatchScope struct {
	instanceIDs []string
}

func NewDeleteBatchScope(instanceIDs []string) *DeleteBatchScope {
	seen := make(map[string]bool, len(instanceIDs))
	clean := make([]string, 0, len(instanceIDs))
	for _, value := range instanceIDs {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		clean = append(clean, value)
	}
	return &DeleteBatchScope{instanceIDs: clean}
}

func (s *DeleteBatchScope) Includes(instanceID string) bool {
	if s == nil {
		return false
	}
	instanceID = strings.TrimSpace(instanceID)
	for _, value := range s.instanceIDs {
		if value == instanceID {
			return true
		}
	}
	return false
}

func (s *DeleteBatchScope) IDs() []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s.instanceIDs...)
}

const DeleteParamConfirmedWithServerPassword = "confirmedWithServerPassword"

func DeleteConfirmedWithServerPassword(req DeleteRequest) bool {
	value, ok := req.Parameters[DeleteParamConfirmedWithServerPassword]
	if !ok {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		v = strings.TrimSpace(v)
		return strings.EqualFold(v, "true") || v == "1"
	default:
		return false
	}
}

type CheckRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
	Actor    string
}

type StorageCleanupEstimateRequest struct {
	Instance      store.AppInstance
	Server        store.Server
	Language      string
	Actor         string
	RetentionDays int
}

type StorageCleanupEstimateResult struct {
	Status        string         `json:"status"`
	RetentionDays int            `json:"retentionDays"`
	ObjectCount   int64          `json:"objectCount"`
	Bytes         int64          `json:"bytes"`
	Source        string         `json:"source"`
	Details       map[string]any `json:"details,omitempty"`
}

type StorageCleanupPolicyRequest struct {
	Instance       store.AppInstance
	Server         store.Server
	Language       string
	Actor          string
	Enabled        bool
	Bucket         string
	Prefix         string
	RetentionDays  int
	ExistingRuleID string
}

type StorageCleanupPolicyResult struct {
	Status        string         `json:"status"`
	Enabled       bool           `json:"enabled"`
	Bucket        string         `json:"bucket"`
	Prefix        string         `json:"prefix,omitempty"`
	RetentionDays int            `json:"retentionDays"`
	RuleID        string         `json:"ruleId,omitempty"`
	Source        string         `json:"source"`
	Details       map[string]any `json:"details,omitempty"`
}

type ArtifactUpdateRequest struct {
	Instance          store.AppInstance
	Server            store.Server
	Language          string
	Actor             string
	ServiceName       string
	ArtifactLocalPath string
	ArtifactFileName  string
}

type ArtifactBundleUpdateRequest struct {
	Instance        store.AppInstance
	Server          store.Server
	Language        string
	Actor           string
	BundleLocalPath string
	BundleFileName  string
}

type ArtifactRollbackRequest struct {
	Instance        store.AppInstance
	Server          store.Server
	Language        string
	Actor           string
	TargetReleaseID string
	Services        []string
	Reason          string
	Force           bool
}

type ArtifactRollbackInspectionRequest struct {
	Instance store.AppInstance
	Release  store.AppRelease
	Manifest map[string]any
}

type ArtifactRollbackInspection struct {
	CurrentServices           []string
	RollbackServices          []string
	RollbackUnavailableReason string
}

type ServiceScaleOutRequest struct {
	Instance    store.AppInstance
	Server      store.Server
	Language    string
	Actor       string
	ServiceName string
	Reason      string
}

type ServiceScaleRequest struct {
	Instance    store.AppInstance
	Server      store.Server
	Language    string
	Actor       string
	ServiceName string
	Replicas    int
	Reason      string
}

type ServiceBatchScaleRequest struct {
	Instance        store.AppInstance
	Server          store.Server
	Language        string
	Actor           string
	DesiredReplicas map[string]int
	Reason          string
}

type ServiceInstallRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
	Actor    string
	Services []string
	Reason   string
}

type RuntimeReconcileRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
	Actor    string
	Reason   string
}

type RuntimeRestartRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
	Actor    string
	Reason   string
}

type RuntimeConfigValues struct {
	AppCPUs                 string  `json:"appCPUs,omitempty"`
	AppMemoryLimit          string  `json:"appMemoryLimit,omitempty"`
	JVMInitialRAMPercentage float64 `json:"jvmInitialRAMPercentage,omitempty"`
	JVMMaxRAMPercentage     float64 `json:"jvmMaxRAMPercentage,omitempty"`
}

type RuntimeConfigPayload struct {
	Global         RuntimeConfigValues            `json:"global"`
	Services       map[string]RuntimeConfigValues `json:"services,omitempty"`
	NacosEphemeral *bool                          `json:"nacosEphemeral,omitempty"`
}

type RuntimeConfigRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
	Actor    string
	Reason   string
	Config   RuntimeConfigPayload
}

type RuntimeCleanupRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
	Actor    string
	Reason   string
}

type RuntimeAgentUninstallRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
	Actor    string
	Reason   string
}

type RuntimeDiagnosticRequest struct {
	ExportID  string
	Instance  store.AppInstance
	Server    store.Server
	Language  string
	Actor     string
	Services  []string
	LocalDate string
	SinceAt   time.Time
	UntilAt   time.Time
}

type RuntimeDiagnosticDeleteRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Export   store.DiagnosticExport
	Language string
	Actor    string
}

type RuntimeDiagnosticStreamRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Export   store.DiagnosticExport
	Language string
	Actor    string
}

type RuntimeDiagnosticEstimateResult struct {
	Services            []RuntimeDiagnosticServiceEstimate `json:"services"`
	LogSource           string                             `json:"logSource"`
	CandidateFiles      int                                `json:"candidateFiles"`
	CandidateScanBytes  int64                              `json:"candidateScanBytes"`
	EstimatedSecondsMin int                                `json:"estimatedSecondsMin"`
	EstimatedSecondsMax int                                `json:"estimatedSecondsMax"`
	MaxFileScanBytes    int64                              `json:"maxFileScanBytes"`
	MaxTotalScanBytes   int64                              `json:"maxTotalScanBytes"`
	MaxFilteredBytes    int64                              `json:"maxFilteredBytes"`
	MaxArchiveBytes     int64                              `json:"maxArchiveBytes"`
	TimeoutSeconds      int                                `json:"timeoutSeconds"`
	ServerTimezone      string                             `json:"serverTimezone"`
	LocalDate           string                             `json:"localDate"`
	DayStartAt          time.Time                          `json:"dayStartAt"`
	DayEndAt            time.Time                          `json:"dayEndAt"`
	CurrentDate         bool                               `json:"currentDate"`
	LocalAvailableBytes int64                              `json:"localAvailableBytes"`
	LocalReadyBytes     int64                              `json:"localReadyBytes"`
	LocalReservedBytes  int64                              `json:"localReservedBytes"`
	LocalQuotaBytes     int64                              `json:"localQuotaBytes"`
	ExpiresAt           time.Time                          `json:"expiresAt"`
	Allowed             bool                               `json:"allowed"`
	BlockReason         string                             `json:"blockReason,omitempty"`
	Warnings            []string                           `json:"warnings,omitempty"`
	FileBytes           int64                              `json:"-"`
	ContainerBytes      int64                              `json:"-"`
	TotalBytes          int64                              `json:"-"`
	RequiredBytes       int64                              `json:"-"`
	AvailableBytes      int64                              `json:"-"`
}

type RuntimeDiagnosticServiceEstimate struct {
	Service            string `json:"service"`
	CandidateFiles     int    `json:"candidateFiles"`
	CandidateScanBytes int64  `json:"candidateScanBytes"`
	FileBytes          int64  `json:"-"`
	ContainerBytes     int64  `json:"-"`
}

type RuntimeDiagnosticError struct {
	Code    string
	Message string
	Details map[string]any
}

func (e *RuntimeDiagnosticError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type ClusterStartRequest struct {
	Instances       []store.AppInstance
	Servers         []store.Server
	Language        string
	Actor           string
	DefaultPassword string
}

type BackupRequest struct {
	Instance      store.AppInstance
	Instances     []store.AppInstance
	Servers       []store.Server
	Language      string
	Actor         string
	RepositoryDir string
	KeepLast      int
	Parameters    map[string]any
}

type BackupSchemaCategory string

const (
	BackupSchemaServerSystem    BackupSchemaCategory = "server-system"
	BackupSchemaClusterMetadata BackupSchemaCategory = "cluster-metadata"
	BackupSchemaBusiness        BackupSchemaCategory = "business"
)

type BackupSchema struct {
	Name              string               `json:"name"`
	Category          BackupSchemaCategory `json:"category"`
	Selectable        bool                 `json:"selectable"`
	SelectedByDefault bool                 `json:"selectedByDefault"`
}

type BackupSchemaCatalog struct {
	InstanceID       string         `json:"instanceId"`
	SourceInstanceID string         `json:"sourceInstanceId"`
	SourceServerID   string         `json:"sourceServerId"`
	Schemas          []BackupSchema `json:"schemas"`
}

func (r BackupRequest) Clone() BackupRequest {
	r.Instances = append([]store.AppInstance(nil), r.Instances...)
	r.Servers = append([]store.Server(nil), r.Servers...)
	r.Parameters = cloneParameters(r.Parameters)
	return r
}

type RestoreRequest struct {
	Instance      store.AppInstance
	Instances     []store.AppInstance
	Servers       []store.Server
	Backup        store.AppBackup
	Language      string
	Actor         string
	RepositoryDir string
	Parameters    map[string]any
}

func (r RestoreRequest) Clone() RestoreRequest {
	r.Instances = append([]store.AppInstance(nil), r.Instances...)
	r.Servers = append([]store.Server(nil), r.Servers...)
	r.Parameters = cloneParameters(r.Parameters)
	return r
}

func cloneParameters(parameters map[string]any) map[string]any {
	if parameters == nil {
		return nil
	}
	cloned := make(map[string]any, len(parameters))
	for key, value := range parameters {
		cloned[key] = cloneParameterValue(value)
	}
	return cloned
}

func cloneParameterValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneParameters(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneParameterValue(item)
		}
		return cloned
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}

type InstanceStatus struct {
	Status  string         `json:"status"`
	Message string         `json:"message,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

type RunContext struct {
	TaskID      string
	Resources   []store.Resource
	Log         Logger
	TargetLog   func(target string) Logger
	Concurrency int
}

func (r RunContext) LoggerForTarget(target string) Logger {
	if r.TargetLog != nil {
		return r.TargetLog(target)
	}
	return r.Log
}

type Dependencies struct {
	Store                          *store.Store
	DefaultPassword                string
	DiagnosticExportDir            string
	DiagnosticExportRetentionHours int
	DiagnosticExportQuotaBytes     int64
}

type Factory func(deps Dependencies) Module

type Module interface {
	Name() string
	Manifest(lang string) Manifest
	PreflightInstall(ctx context.Context, req InstallRequest, resources []store.Resource) (PreflightResult, error)
	PlanInstall(ctx context.Context, req InstallRequest, resources []store.Resource) ([]InstallStepPlan, error)
	ValidateInstall(ctx context.Context, req InstallRequest, resources []store.Resource) error
	Install(ctx context.Context, req InstallRequest, run RunContext) error
}

type DeleteModule interface {
	PlanDelete(ctx context.Context, req DeleteRequest) ([]InstallStepPlan, error)
	Delete(ctx context.Context, req DeleteRequest, run RunContext) error
}

// BatchDeleteModule validates a complete, immutable deletion selection before
// the worker performs the first remote mutation.
type BatchDeleteModule interface {
	PreflightDeleteBatch(context.Context, []DeleteRequest) error
}

type CheckModule interface {
	PlanCheck(ctx context.Context, req CheckRequest) ([]InstallStepPlan, error)
	Check(ctx context.Context, req CheckRequest, run RunContext) (InstanceStatus, error)
}

type StorageCleanupEstimateModule interface {
	EstimateStorageCleanup(ctx context.Context, req StorageCleanupEstimateRequest, run RunContext) (StorageCleanupEstimateResult, error)
}

type StorageCleanupPolicyModule interface {
	ApplyStorageCleanupPolicy(ctx context.Context, req StorageCleanupPolicyRequest, run RunContext) (StorageCleanupPolicyResult, error)
}

type ArtifactUpdateModule interface {
	PlanArtifactUpdate(ctx context.Context, req ArtifactUpdateRequest) ([]InstallStepPlan, error)
	ValidateArtifactUpdate(ctx context.Context, req ArtifactUpdateRequest) error
	UpdateArtifact(ctx context.Context, req ArtifactUpdateRequest, run RunContext) error
}

type ArtifactBundleUpdateModule interface {
	PlanArtifactBundleUpdate(ctx context.Context, req ArtifactBundleUpdateRequest) ([]InstallStepPlan, error)
	ValidateArtifactBundleUpdate(ctx context.Context, req ArtifactBundleUpdateRequest) error
	UpdateArtifactBundle(ctx context.Context, req ArtifactBundleUpdateRequest, run RunContext) error
}

type ArtifactRollbackModule interface {
	PlanArtifactRollback(ctx context.Context, req ArtifactRollbackRequest) ([]InstallStepPlan, error)
	ValidateArtifactRollback(ctx context.Context, req ArtifactRollbackRequest) error
	RollbackArtifact(ctx context.Context, req ArtifactRollbackRequest, run RunContext) error
}

type ArtifactRollbackInspectionModule interface {
	InspectArtifactRollback(context.Context, ArtifactRollbackInspectionRequest) ArtifactRollbackInspection
}

type ServiceScaleOutModule interface {
	ScaleOutService(ctx context.Context, req ServiceScaleOutRequest, run RunContext) error
}

type ServiceScaleModule interface {
	ScaleService(ctx context.Context, req ServiceScaleRequest, run RunContext) error
}

type ServiceBatchScaleModule interface {
	ScaleServices(ctx context.Context, req ServiceBatchScaleRequest, run RunContext) error
}

type ServiceInstallModule interface {
	InstallServices(ctx context.Context, req ServiceInstallRequest, run RunContext) error
}

type RuntimeReconcileModule interface {
	ReconcileRuntime(ctx context.Context, req RuntimeReconcileRequest, run RunContext) error
}

type RuntimeRestartModule interface {
	RestartRuntime(ctx context.Context, req RuntimeRestartRequest, run RunContext) error
}

type RuntimeConfigModule interface {
	ValidateRuntimeConfig(ctx context.Context, req RuntimeConfigRequest) error
	ApplyRuntimeConfig(ctx context.Context, req RuntimeConfigRequest, run RunContext) error
}

type RuntimeCleanupModule interface {
	CleanupRuntimeStalePods(ctx context.Context, req RuntimeCleanupRequest, run RunContext) error
}

type RuntimeAgentUninstallModule interface {
	UninstallRuntimeAgent(ctx context.Context, req RuntimeAgentUninstallRequest, run RunContext) error
}

type RuntimeDiagnosticsModule interface {
	EstimateRuntimeDiagnostics(context.Context, RuntimeDiagnosticRequest, RunContext) (RuntimeDiagnosticEstimateResult, error)
	ExportRuntimeDiagnostics(context.Context, RuntimeDiagnosticRequest, RunContext) error
	DeleteRuntimeDiagnosticExport(context.Context, RuntimeDiagnosticDeleteRequest, RunContext) error
	StreamRuntimeDiagnosticExport(context.Context, RuntimeDiagnosticStreamRequest, io.Writer) (int64, error)
}

type ClusterStartModule interface {
	PlanClusterStart(ctx context.Context, req ClusterStartRequest) ([]InstallStepPlan, error)
	StartCluster(ctx context.Context, req ClusterStartRequest, run RunContext) error
}

type BackupModule interface {
	PlanBackup(context.Context, BackupRequest) ([]InstallStepPlan, error)
	Backup(context.Context, BackupRequest, RunContext) error
}

type BackupSchemaModule interface {
	DiscoverBackupSchemas(context.Context, BackupRequest) (BackupSchemaCatalog, error)
}

type RestoreModule interface {
	PlanRestore(context.Context, RestoreRequest) ([]InstallStepPlan, error)
	Restore(context.Context, RestoreRequest, RunContext) error
}
