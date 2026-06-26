package offlineapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
)

type Store interface {
	GetServer(id string, includeSecret bool) (store.Server, error)
	SaveAppInstance(v store.AppInstance) (store.AppInstance, error)
}

type Definition struct {
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
	ResourceMatchers      []string
	Steps                 []StepDefinition
	Language              string
}

type DefinitionFunc func(lang string) Definition

type StepDefinition struct {
	Name  string
	Title string
}

type Module struct {
	definitionFor DefinitionFunc
	store         Store
}

type stepRecorder interface {
	StartTarget(target string)
	FinishTarget(target, status, errText string)
	StartStep(target, name, title string, order int)
	FinishStep(target, name, status, errText string)
}

func New(def Definition, s Store) Module {
	return NewDynamic(func(string) Definition { return def }, s)
}

func NewDynamic(fn DefinitionFunc, s Store) Module {
	return Module{definitionFor: fn, store: s}
}

func normalizeDefinition(def Definition) Definition {
	if def.InstallName == "" {
		def.InstallName = def.Name
	}
	if def.ResourceApp == "" {
		def.ResourceApp = def.InstallName
	}
	if len(def.RequiredResourceParts) == 0 {
		def.RequiredResourceParts = []string{"backend"}
	}
	if len(def.Steps) == 0 {
		def.Steps = []StepDefinition{
			{Name: "load-server", Title: "load target server"},
			{Name: "verify-resource", Title: "verify offline resource"},
			{Name: "stage-installer", Title: "stage module installer"},
			{Name: "record-instance", Title: "record app instance"},
		}
	}
	return def
}

func (m Module) definition(lang string) Definition {
	if m.definitionFor == nil {
		return normalizeDefinition(Definition{})
	}
	def := normalizeDefinition(m.definitionFor(lang))
	def.Language = lang
	return def
}

func (m Module) Name() string {
	return m.definition("").Name
}

func (m Module) Manifest(lang string) registry.Manifest {
	def := m.definition(lang)
	return registry.Manifest{
		Name:                  def.Name,
		Title:                 def.Title,
		Icon:                  def.Icon,
		Category:              def.Category,
		CategoryLabel:         def.CategoryLabel,
		SourceLabel:           def.SourceLabel,
		FallbackVersion:       def.FallbackVersion,
		Description:           def.Description,
		InstallName:           def.InstallName,
		ResourceApp:           def.ResourceApp,
		RequiresServer:        def.RequiresServer,
		SupportsMultiTarget:   def.SupportsMultiTarget,
		BackendReady:          def.BackendReady,
		RequiredResourceParts: def.RequiredResourceParts,
		Capabilities:          def.Capabilities,
	}
}

func (m Module) PreflightInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) (registry.PreflightResult, error) {
	def := m.definition(req.Language)
	if ctx.Err() != nil {
		return registry.PreflightResult{}, ctx.Err()
	}
	if _, err := m.selectResource(def, resources, req.Version); err != nil {
		return registry.PreflightResult{}, err
	}
	warnings := []string{
		i18n.Text(req.Language, "offline.sharedLifecycleWarning", def.Name),
	}
	return registry.PreflightResult{Warnings: warnings}, nil
}

func (m Module) PlanInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) ([]registry.InstallStepPlan, error) {
	def := m.definition(req.Language)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	targets := m.targets(req)
	plan := make([]registry.InstallStepPlan, 0, len(targets)*len(def.Steps))
	for _, target := range targets {
		for idx, step := range def.Steps {
			plan = append(plan, registry.InstallStepPlan{
				Target: target,
				Name:   step.Name,
				Title:  step.Title,
				Order:  idx + 1,
			})
		}
	}
	return plan, nil
}

func (m Module) ValidateInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) error {
	def := m.definition(req.Language)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	targets := req.TargetServerIDs()
	if def.RequiresServer && len(targets) == 0 {
		return fmt.Errorf(i18n.Text(req.Language, "offline.targetRequired"), def.Name)
	}
	if !def.SupportsMultiTarget && len(targets) > 1 {
		return fmt.Errorf(i18n.Text(req.Language, "offline.singleTargetOnly"), def.Name)
	}
	res, err := m.selectResource(def, resources, req.Version)
	if err != nil {
		return err
	}
	return verifyResourceWithLanguage(res, req.Language)
}

func (m Module) Install(ctx context.Context, req registry.InstallRequest, run registry.RunContext) error {
	def := m.definition(req.Language)
	resource, err := m.selectResource(def, run.Resources, req.Version)
	if err != nil {
		return err
	}
	if err := verifyResourceWithLanguage(resource, req.Language); err != nil {
		return err
	}
	recorder, _ := run.Log.(stepRecorder)
	var failures []string
	for _, target := range m.targets(req) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if recorder != nil {
			recorder.StartTarget(target)
		}
		var server store.Server
		if err := m.runStep(def, recorder, target, 1, func() error {
			if !def.RequiresServer {
				return nil
			}
			var loadErr error
			server, loadErr = m.store.GetServer(target, true)
			return loadErr
		}, run.Log); err != nil {
			failures = append(failures, finishFailure(recorder, target, err))
			continue
		}
		if err := m.runStep(def, recorder, target, 2, func() error {
			run.Log.Info(i18n.Text(req.Language, "offline.selectedResource"), def.Name, resource.Path)
			return nil
		}, run.Log); err != nil {
			failures = append(failures, finishFailure(recorder, target, err))
			continue
		}
		if err := m.runStep(def, recorder, target, 3, func() error {
			run.Log.Info(i18n.Text(req.Language, "offline.installerStaged"), def.Name)
			return nil
		}, run.Log); err != nil {
			failures = append(failures, finishFailure(recorder, target, err))
			continue
		}
		if err := m.runStep(def, recorder, target, 4, func() error {
			metadata, _ := json.Marshal(map[string]any{
				"resourcePath": resource.Path,
				"rpmCount":     resource.RPMCount,
				"parameters":   req.Parameters,
				"stageOnly":    true,
			})
			serverID := target
			if !def.RequiresServer {
				serverID = ""
			}
			status := "staged"
			if server.ID != "" {
				serverID = server.ID
			}
			_, saveErr := m.store.SaveAppInstance(store.AppInstance{
				App:      def.Name,
				Version:  resource.Version,
				ServerID: serverID,
				Status:   status,
				Topology: req.Topology,
				Metadata: string(metadata),
			})
			return saveErr
		}, run.Log); err != nil {
			failures = append(failures, finishFailure(recorder, target, err))
			continue
		}
		if recorder != nil {
			recorder.FinishTarget(target, "success", "")
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf(i18n.Text(req.Language, "offline.installFailures"), def.Name, len(failures), strings.Join(failures, "; "))
	}
	return nil
}

func (m Module) targets(req registry.InstallRequest) []string {
	targets := req.TargetServerIDs()
	if len(targets) == 0 {
		return []string{"control-plane"}
	}
	return targets
}

func (m Module) runStep(def Definition, recorder stepRecorder, target string, stepIndex int, fn func() error, log registry.Logger) error {
	step := def.Steps[stepIndex-1]
	if recorder != nil {
		recorder.StartStep(target, step.Name, step.Title, stepIndex)
	}
	log.Info(i18n.Text(def.Language, "offline.stepStart"), target, stepIndex, len(def.Steps), step.Title)
	if err := fn(); err != nil {
		if recorder != nil {
			recorder.FinishStep(target, step.Name, "failed", err.Error())
		}
		log.Error(i18n.Text(def.Language, "offline.stepFailed"), target, stepIndex, len(def.Steps), step.Title, err)
		return err
	}
	if recorder != nil {
		recorder.FinishStep(target, step.Name, "success", "")
	}
	log.Info(i18n.Text(def.Language, "offline.stepDone"), target, stepIndex, len(def.Steps), step.Title)
	return nil
}

func finishFailure(recorder stepRecorder, target string, err error) string {
	msg := fmt.Sprintf("%s: %v", target, err)
	if recorder != nil {
		recorder.FinishTarget(target, "failed", msg)
	}
	return msg
}

func (m Module) selectResource(def Definition, resources []store.Resource, version string) (store.Resource, error) {
	var candidates []store.Resource
	for _, res := range resources {
		if res.App != def.ResourceApp || res.Part != "backend" {
			continue
		}
		if version != "" && version != "latest" && res.Version != version {
			continue
		}
		if !m.matchesResource(def, res.Path) {
			continue
		}
		candidates = append(candidates, res)
	}
	if len(candidates) == 0 {
		if version == "" {
			version = "latest"
		}
		return store.Resource{}, fmt.Errorf(i18n.Text(def.Language, "offline.noResource"), def.Name, version)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Version == candidates[j].Version {
			return candidates[i].Path < candidates[j].Path
		}
		return candidates[i].Version < candidates[j].Version
	})
	return candidates[len(candidates)-1], nil
}

func (m Module) matchesResource(def Definition, path string) bool {
	name := strings.ToLower(filepath.Base(path))
	if strings.HasSuffix(name, ".sha256sum") || strings.HasSuffix(name, ".minisig") {
		return false
	}
	if len(def.ResourceMatchers) == 0 {
		return true
	}
	for _, matcher := range def.ResourceMatchers {
		if strings.Contains(name, strings.ToLower(matcher)) {
			return true
		}
	}
	return false
}

func verifyResource(res store.Resource) error {
	return verifyResourceWithLanguage(res, "")
}

func verifyResourceWithLanguage(res store.Resource, lang string) error {
	if _, err := os.Stat(res.Path); err != nil {
		return err
	}
	if strings.TrimSpace(res.SHA256) == "" {
		return nil
	}
	sum, err := sha256File(res.Path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(sum, res.SHA256) {
		return fmt.Errorf(i18n.Text(lang, "offline.shaMismatch"), res.Path, res.SHA256, sum)
	}
	return nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
