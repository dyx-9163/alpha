package registry

import (
	"context"
	"strings"

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

type ServiceScaleOutRequest struct {
	Instance    store.AppInstance
	Server      store.Server
	Language    string
	Actor       string
	ServiceName string
	Reason      string
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

type RuntimeConfigValues struct {
	AppCPUs                 string  `json:"appCPUs,omitempty"`
	AppMemoryLimit          string  `json:"appMemoryLimit,omitempty"`
	JVMInitialRAMPercentage float64 `json:"jvmInitialRAMPercentage,omitempty"`
	JVMMaxRAMPercentage     float64 `json:"jvmMaxRAMPercentage,omitempty"`
}

type RuntimeConfigPayload struct {
	Global   RuntimeConfigValues            `json:"global"`
	Services map[string]RuntimeConfigValues `json:"services,omitempty"`
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

type ClusterStartRequest struct {
	Instances       []store.AppInstance
	Servers         []store.Server
	Language        string
	Actor           string
	DefaultPassword string
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
	Store           *store.Store
	DefaultPassword string
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

type CheckModule interface {
	PlanCheck(ctx context.Context, req CheckRequest) ([]InstallStepPlan, error)
	Check(ctx context.Context, req CheckRequest, run RunContext) (InstanceStatus, error)
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

type ServiceScaleOutModule interface {
	ScaleOutService(ctx context.Context, req ServiceScaleOutRequest, run RunContext) error
}

type ServiceInstallModule interface {
	InstallServices(ctx context.Context, req ServiceInstallRequest, run RunContext) error
}

type RuntimeReconcileModule interface {
	ReconcileRuntime(ctx context.Context, req RuntimeReconcileRequest, run RunContext) error
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

type ClusterStartModule interface {
	PlanClusterStart(ctx context.Context, req ClusterStartRequest) ([]InstallStepPlan, error)
	StartCluster(ctx context.Context, req ClusterStartRequest, run RunContext) error
}
