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
	Name                  string
	Title                 string
	Icon                  string
	Category              string
	CategoryLabel         string
	SourceLabel           string
	FallbackVersion       string
	Description           string
	InstallName           string
	ResourceApp           string
	RequiresServer        bool
	SupportsMultiTarget   bool
	BackendReady          bool
	RequiredResourceParts []string
	Capabilities          []string
	Topologies            []Topology
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

type InstanceStatus struct {
	Status  string         `json:"status"`
	Message string         `json:"message,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

type RunContext struct {
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
	Store *store.Store
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
