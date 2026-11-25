package minio

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installflow"
	"aifar-deployment/backend/internal/resourcekit"
	"aifar-deployment/backend/internal/store"
)

const (
	offlineMinioVersion = "2025-10-15T17-29-55Z"
	offlineSiloVersion  = "20261112"
	offlineSiloRelease  = "2026-11-12T00-00-00Z"
)

//go:embed templates/upgrade/silo.sh
var offlineSiloScript string

var offlineSiloFiles = [2]struct{ name, digest string }{
	{"silo_20261112000000.0.0_linux_amd64.tar.gz", "378adce81515d31d48d8ac9b79effc62980e964c470fb06cc3ee8984786c2f4d"},
	{"mcli_20261112000000.0.0_linux_amd64.tar.gz", "68875c7fdd06a76af12b74753506df65226d1bec65a405d2617d76448c229ba2"},
}

type offlineSiloBundle [2]string

type offlineUpgradeGroupStore interface {
	ListAppInstances() ([]store.AppInstance, error)
}

type offlineSiloCheckpointReader interface {
	GetSiloOfflineUpgradeCheckpoint(string) (store.SiloOfflineUpgradeCheckpoint, error)
}

type offlineUpgradeDataStore interface {
	offlineUpgradeGroupStore
	offlineSiloCheckpointReader
	CreateSiloOfflineUpgradeCheckpoint(string, store.SiloOfflineUpgradeCheckpoint) error
	DiscardSiloOfflineUpgradeCheckpoint(string, string) error
	CompleteSiloOfflineUpgrade(string, string, []store.AppInstanceVersionUpdate) error
}

type offlineSiloMetadata struct {
	GroupID     string   `json:"replicationGroupId"`
	Mode        string   `json:"replicationMode"`
	PeerID      string   `json:"peerServerId"`
	Service     string   `json:"serviceName"`
	Endpoint    string   `json:"endpoint"`
	Peer        string   `json:"peerEndpoint"`
	APIPort     int      `json:"apiPort"`
	ConsolePort int      `json:"consolePort"`
	DataDir     string   `json:"dataDir"`
	DataDirs    []string `json:"dataDirs"`
	Buckets     []string `json:"replicationBuckets"`
}

type offlineSiloNode struct {
	instance store.AppInstance
	server   store.Server
	meta     offlineSiloMetadata
	root     string
	backup   string
	stage    string
	before   []string
}

var (
	siloTaskIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,96}$`)
	siloStagePattern  = regexp.MustCompile(`^/tmp/aifar-silo\.[a-zA-Z0-9]{8}$`)
	siloPathPattern   = regexp.MustCompile(`^/[a-zA-Z0-9._/-]+$`)
	siloErrorPattern  = regexp.MustCompile(`(?m)^AIFAR_SILO_LINE=([0-9]+)$`)
)

func (m Module) ResolveOfflineUpgradeGroup(seed store.AppInstance) ([]store.AppInstance, error) {
	return m.service.ResolveOfflineUpgradeGroup(seed)
}

func (m Module) RunOfflineUpgrade(ctx context.Context, seed store.AppInstance, resources []store.Resource, run registry.RunContext) error {
	return m.service.RunOfflineUpgrade(ctx, seed, resources, run)
}

func (m Module) PlanOfflineUpgrade(_ store.AppInstance, peers []store.AppInstance, language string) []registry.InstallStepPlan {
	steps := siloUpgradeSteps(language)
	plans := make([]registry.InstallStepPlan, 0, len(steps)*len(peers))
	for _, peer := range peers {
		for i, step := range steps {
			plans = append(plans, registry.InstallStepPlan{Target: peer.ServerID, Name: step.Name, Title: step.Title, Order: i + 1})
		}
	}
	return plans
}

func siloUpgradeSteps(language string) []stepDef {
	return []stepDef{
		{Name: "stage", Title: siloText(language, "验证资源并暂存两端二进制", "Verify assets and stage both nodes")},
		{Name: "before", Title: siloText(language, "核对旧服务、存储和复制状态", "Verify source service, volumes, and replication")},
		{Name: "stop", Title: siloText(language, "停止两个旧 MinIO 服务", "Stop both MinIO services")},
		{Name: "backup", Title: siloText(language, "创建并校验两端冷备", "Create and verify both cold backups")},
		{Name: "verify-backup", Title: siloText(language, "复核两端冷备校验和", "Recheck both cold backup checksums")},
		{Name: "switch", Title: siloText(language, "替换两端服务二进制", "Switch both server binaries")},
		{Name: "start", Title: siloText(language, "启动两端 Silo 服务", "Start both Silo services")},
		{Name: "after", Title: siloText(language, "核对 Silo 版本和保留的复制规则", "Verify Silo version and retained replication rules")},
	}
}

func (s Service) ResolveOfflineUpgradeGroup(seed store.AppInstance) ([]store.AppInstance, error) {
	data, ok := s.store.(offlineUpgradeGroupStore)
	if !ok {
		return nil, errors.New("MinIO replication group lookup unavailable")
	}
	var seedMeta offlineSiloMetadata
	if seed.App != "minio" || seed.ID == "" || json.Unmarshal([]byte(seed.Metadata), &seedMeta) != nil || strings.TrimSpace(seedMeta.GroupID) == "" {
		return nil, errors.New("MinIO bucket replication group identity missing")
	}
	resuming := false
	if journal, ok := s.store.(offlineSiloCheckpointReader); ok {
		_, err := journal.GetSiloOfflineUpgradeCheckpoint(seedMeta.GroupID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		resuming = err == nil
	}
	all, err := data.ListAppInstances()
	if err != nil {
		return nil, err
	}
	var group []store.AppInstance
	var metadata []offlineSiloMetadata
	for _, instance := range all {
		if instance.App != "minio" {
			continue
		}
		// Read membership independently so malformed member details cannot hide a third row.
		var membership struct {
			GroupID string `json:"replicationGroupId"`
		}
		if json.Unmarshal([]byte(instance.Metadata), &membership) != nil || membership.GroupID != seedMeta.GroupID {
			continue
		}
		meta, err := parseOfflineSiloMember(instance, resuming)
		if err != nil {
			return nil, err
		}
		group = append(group, instance)
		metadata = append(metadata, meta)
	}
	if len(group) != 2 || group[0].ServerID == group[1].ServerID || group[0].ID == group[1].ID ||
		(metadata[0].PeerID != group[1].ServerID || metadata[1].PeerID != group[0].ServerID) ||
		metadata[0].Peer != metadata[1].Endpoint || metadata[1].Peer != metadata[0].Endpoint ||
		metadata[0].Endpoint == metadata[1].Endpoint || !slices.Equal(metadata[0].Buckets, metadata[1].Buckets) {
		return nil, errors.New("MinIO bucket replication group must contain exactly two reciprocal Panel-managed members")
	}
	for index, member := range group {
		if member.ID != seed.ID {
			continue
		}
		if member.ServerID != seed.ServerID || !member.UpdatedAt.Equal(seed.UpdatedAt) || member.Version != seed.Version {
			break
		}
		if index == 1 {
			group[0], group[1] = group[1], group[0]
		}
		return group, nil
	}
	return nil, errors.New("the selected MinIO instance has changed")
}

func parseOfflineSiloMember(instance store.AppInstance, resuming bool) (offlineSiloMetadata, error) {
	var m offlineSiloMetadata
	if instance.Version != offlineMinioVersion || (!resuming && instance.Status != "installed" && instance.Status != "available") || instance.ServerID == "" ||
		normalizeTopology(instance.Topology) != "bucket-replication" ||
		json.Unmarshal([]byte(instance.Metadata), &m) != nil ||
		m.Mode != "bucket" || m.Service != "aifar-minio" ||
		m.APIPort < 1 || m.APIPort > 65535 || m.ConsolePort < 1 || m.ConsolePort > 65535 ||
		m.APIPort == m.ConsolePort || len(m.Buckets) == 0 || len(m.DataDirs) == 0 ||
		m.DataDir != m.DataDirs[0] {
		return m, errors.New("unsupported MinIO source instance or incomplete Panel metadata")
	}
	endpoint, err := url.Parse(m.Endpoint)
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() == "" || endpoint.Port() != strconv.Itoa(m.APIPort) ||
		endpoint.Path != "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return m, errors.New("unsupported MinIO endpoint")
	}
	for index, volume := range m.DataDirs {
		if !validSiloPath(volume) {
			return m, errors.New("unsupported MinIO data directory")
		}
		for _, previous := range m.DataDirs[:index] {
			if siloPathsOverlap(previous, volume) {
				return m, errors.New("MinIO data directories must be distinct and cannot contain each other")
			}
		}
	}
	slices.Sort(m.Buckets)
	for i, bucket := range m.Buckets {
		if strings.TrimSpace(bucket) != bucket || validateMinioBucketName(bucket) != nil || (i > 0 && m.Buckets[i-1] == bucket) {
			return m, errors.New("invalid or duplicate MinIO replication bucket")
		}
	}
	return m, nil
}

func validSiloPath(value string) bool {
	return value != "/" && path.Clean(value) == value && siloPathPattern.MatchString(value)
}

func siloPathsOverlap(first, second string) bool {
	return first == second || strings.HasPrefix(first, second+"/") || strings.HasPrefix(second, first+"/")
}

func selectOfflineSiloBundle(resources []store.Resource) (offlineSiloBundle, error) {
	var bundle offlineSiloBundle
	for index, asset := range offlineSiloFiles {
		for _, resource := range resources {
			if resource.App != "silo" || resource.Version != offlineSiloVersion || resource.Part != "backend" || filepath.Base(resource.Path) != asset.name {
				continue
			}
			if bundle[index] != "" || !strings.EqualFold(resource.SHA256, asset.digest) {
				return bundle, fmt.Errorf("ambiguous or invalid Silo asset checksum: %s", asset.name)
			}
			bundle[index] = resource.Path
		}
		if bundle[index] == "" {
			return bundle, fmt.Errorf("required Silo asset is missing: %s", asset.name)
		}
		if err := resourcekit.VerifySHA256(bundle[index], asset.digest, asset.name); err != nil {
			return bundle, err
		}
	}
	return bundle, nil
}

func (s Service) RunOfflineUpgrade(ctx context.Context, seed store.AppInstance, resources []store.Resource, run registry.RunContext) error {
	data, ok := s.store.(offlineUpgradeDataStore)
	if !ok || s.remote == nil || !siloTaskIDPattern.MatchString(run.TaskID) {
		return errors.New("MinIO offline migration dependencies unavailable")
	}
	group, err := s.ResolveOfflineUpgradeGroup(seed)
	if err != nil {
		return err
	}
	bundle, err := selectOfflineSiloBundle(resources)
	if err != nil {
		return err
	}
	return s.runOfflineUpgrade(ctx, data, group, bundle, run)
}

func (s Service) runOfflineUpgrade(ctx context.Context, data offlineUpgradeDataStore, group []store.AppInstance, bundle offlineSiloBundle, run registry.RunContext) (resultErr error) {
	var seedMeta offlineSiloMetadata
	if len(group) != 2 || json.Unmarshal([]byte(group[0].Metadata), &seedMeta) != nil || seedMeta.GroupID == "" {
		return errors.New("invalid Silo upgrade group")
	}
	checkpoint, err := data.GetSiloOfflineUpgradeCheckpoint(seedMeta.GroupID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	resuming := err == nil
	operationID := run.TaskID
	if resuming {
		operationID = checkpoint.TaskID
	}
	nodes, err := s.loadOfflineSiloNodes(group, operationID, resuming)
	if err != nil {
		return err
	}
	if resuming {
		if err := matchSiloCheckpoint(nodes, checkpoint); err != nil {
			return err
		}
		if log := run.LoggerForTarget(nodes[0].server.ID); log != nil {
			log.Info(siloText(run.Language, "正在复核原迁移任务 %s 并复用其冷备", "Checking original migration %s and reusing its cold backups"), operationID)
		}
	}

	recorder, _ := run.Log.(stepRecorder)
	var resumedStates []offlineSiloState
	var recoverOnError, cutoverVerified bool
	defer func() {
		cleanup := context.WithoutCancel(ctx)
		if resultErr != nil {
			switch {
			case cutoverVerified:
				resultErr = errors.Join(resultErr, errors.New(siloText(run.Language,
					"两端 Silo 已通过验收并保持运行。请通过原断点重试面板登记后再放行业务写入。",
					"Both Silo services remain running after verification. Retry registration from the original checkpoint before reopening writes.")))
			case recoverOnError:
				resultErr = errors.Join(resultErr, s.recoverOfflineSiloGroup(cleanup, data, seedMeta.GroupID, operationID, nodes, run.Language))
			}
			if recoverOnError || cutoverVerified || resuming {
				resultErr = fmt.Errorf("%w [%s: %s=%s, %s=%s]", resultErr,
					siloText(run.Language, "原冷备目录", "Original cold backup directories"),
					nodes[0].server.ID, nodes[0].backup, nodes[1].server.ID, nodes[1].backup)
			}
		}
		for _, node := range nodes {
			if node.stage != "" {
				_, _ = s.runOfflineSiloCommand(cleanup, node, "clean-stage", run.Language)
			}
			if resultErr != nil {
				finishTarget(recorder, node.server.ID, "failed", resultErr.Error())
			} else {
				finishTarget(recorder, node.server.ID, "success", "")
			}
		}
	}()
	for _, node := range nodes {
		installflow.StartTarget(recorder, node.server.ID)
	}

	if resuming {
		resumedStates, err = s.readOfflineSiloStates(ctx, nodes, run.Language)
		if err != nil {
			return err
		}
		for _, state := range resumedStates {
			if state.restoreAttempted || (state.runtime == "old-running" && state.backupComplete) {
				return s.recoverOfflineSiloGroup(context.WithoutCancel(ctx), data, seedMeta.GroupID, operationID, nodes, run.Language)
			}
		}
		if err := validateSiloResumeBarrier(resumedStates); err != nil {
			return err
		}
	}

	steps := siloUpgradeSteps(run.Language)
	for stepIndex, step := range steps {
		for index := range nodes {
			node := &nodes[index]
			runner := newStepRunnerWithSteps(run.LoggerForTarget(node.server.ID), recorder, node.server.ID, CopyFor(run.Language), steps)
			if err := runner(stepIndex+1, step.Name, step.Title, func() error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if resuming && resumedStates[index].skip(step.Name) {
					return nil
				}
				if step.Name == "stage" {
					return s.stageOfflineSiloAssets(ctx, node, bundle, run)
				}
				if step.Name == "stop" {
					recoverOnError = true
				}
				raw, err := s.runOfflineSiloCommand(ctx, *node, step.Name, run.Language)
				if err != nil {
					return err
				}
				if step.Name == "before" || step.Name == "after" {
					state, err := parseOfflineSiloReplication(raw, node.meta)
					if err != nil {
						return errors.New(siloText(run.Language, "无法核对桶清单、复制规则或复制积压", "Could not confirm the bucket inventory, replication rules or drained queue"))
					}
					if err := s.validateOfflineSiloInventory(ctx, *node, state, run.Language); err != nil {
						return err
					}
					if step.Name == "before" {
						node.before = state
					} else if !slices.Equal(node.before, state) {
						return errors.New(siloText(run.Language, "桶清单、复制规则或版本化配置发生变化", "Bucket inventory, replication rules or bucket versioning changed"))
					}
				}
				return nil
			}); err != nil {
				return err
			}
		}
		if step.Name == "before" && !resuming {
			if !reciprocalSiloBaselines(nodes) {
				return errors.New(siloText(run.Language, "两端 MinIO 部署标识与对端不一致", "MinIO deployment identities did not match the two reciprocal endpoints"))
			}
			checkpoint = newSiloCheckpoint(operationID, nodes)
			if err := data.CreateSiloOfflineUpgradeCheckpoint(seedMeta.GroupID, checkpoint); err != nil {
				return err
			}
		}
		if resuming && step.Name == "before" {
			// The original attempt may already have shut down either node.
			recoverOnError = true
		}
	}
	if err := s.verifyOfflineSiloReplication(ctx, nodes, operationID, run.Language); err != nil {
		return err
	}
	cutoverVerified = true
	if err := ctx.Err(); err != nil {
		return err
	}
	updates := make([]store.AppInstanceVersionUpdate, 0, len(nodes))
	for _, node := range nodes {
		updates = append(updates, store.AppInstanceVersionUpdate{
			Expected: node.instance, Version: offlineSiloVersion,
			Metadata: map[string]any{"serverProduct": "silo", "offlineUpgradeTaskId": operationID, "offlineUpgradeBackupDir": node.backup},
		})
	}
	return data.CompleteSiloOfflineUpgrade(seedMeta.GroupID, operationID, updates)
}

func (s Service) stageOfflineSiloAssets(ctx context.Context, node *offlineSiloNode, bundle offlineSiloBundle, run registry.RunContext) error {
	stage, err := s.runOfflineSiloCommand(ctx, *node, "stage-dir", run.Language)
	if err != nil {
		return err
	}
	node.stage = strings.TrimSpace(stage)
	if !siloStagePattern.MatchString(node.stage) {
		node.stage = ""
		return errors.New(siloText(run.Language, "暂存目录无效", "Invalid remote staging directory"))
	}
	for index, asset := range offlineSiloFiles {
		uploadCtx, cancel := context.WithTimeout(ctx, 45*time.Minute)
		err := s.remote.UploadFile(uploadCtx, node.server, bundle[index], path.Join(node.stage, asset.name), 0600)
		cancel()
		if err != nil {
			return fmt.Errorf("%s %s", siloText(run.Language, "无法上传离线资源", "Could not upload offline asset"), asset.name)
		}
	}
	if _, err := s.runOfflineSiloCommand(ctx, *node, "stage", run.Language); err != nil {
		return err
	}
	// The root-owned candidate copy is complete. Drop the upload before
	// any stoppage so a later Panel exit does not strand these archives.
	if _, err := s.runOfflineSiloCommand(ctx, *node, "clean-stage", run.Language); err == nil {
		node.stage = ""
	}
	if log := run.LoggerForTarget(node.server.ID); log != nil {
		log.Info("%s %s", siloText(run.Language, "冷备目录", "Cold backup directory"), node.backup)
	}
	return nil
}

func (s Service) runOfflineSiloCommand(ctx context.Context, node offlineSiloNode, phase, language string) (string, error) {
	header := "# aifar-silo " + phase + "\n"
	budget := siloPhaseBudget(phase, len(node.meta.Buckets))
	var command string
	switch phase {
	case "stage-dir":
		command = header + "umask 077\nmktemp -d /tmp/aifar-silo.XXXXXXXX"
	case "clean-stage":
		command = header + "rm -f -- " + installerkit.ShellQuote(path.Join(node.stage, offlineSiloFiles[0].name)) + " " +
			installerkit.ShellQuote(path.Join(node.stage, offlineSiloFiles[1].name)) + "\nrmdir -- " + installerkit.ShellQuote(node.stage)
	default:
		peer, _ := url.Parse(node.meta.Peer)
		localURL := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(node.meta.APIPort))
		args := []string{phase, node.root, node.backup, node.stage, localURL, peer.String(), strconv.Itoa(node.meta.ConsolePort),
			offlineMinioVersion, offlineSiloRelease, offlineSiloFiles[0].name, offlineSiloFiles[0].digest,
			offlineSiloFiles[1].name, offlineSiloFiles[1].digest, strconv.Itoa(len(node.meta.DataDirs))}
		args = append(args, node.meta.DataDirs...)
		args = append(args, node.meta.Buckets...)
		for i := range args {
			args[i] = installerkit.ShellQuote(args[i])
		}
		command = header + "aifar_sudo=\nif [ \"$(id -u)\" -ne 0 ]; then aifar_sudo='sudo -n'; fi\n" +
			fmt.Sprintf("$aifar_sudo timeout --signal=TERM %ds bash -s -- ", int64(budget/time.Second)) +
			strings.Join(args, " ") + " <<'AIFAR_SILO_SCRIPT'\n" + offlineSiloScript + "\nAIFAR_SILO_SCRIPT"
	}
	phaseCtx, cancel := context.WithTimeout(ctx, budget+30*time.Second)
	defer cancel()
	result, err := s.remote.Run(phaseCtx, node.server, command)
	if err != nil || result.ExitCode != 0 {
		// The old mc may include credential-bearing remote targets in its output.
		message := fmt.Sprintf("%s (%s, %s)", siloText(language, "远程升级步骤失败", "Remote upgrade step failed"), node.server.ID, phase)
		var exit interface{ ExitStatus() int }
		if errors.Is(phaseCtx.Err(), context.DeadlineExceeded) || result.ExitCode == 124 || (errors.As(err, &exit) && exit.ExitStatus() == 124) {
			message += " " + siloText(language, "已到阶段截止时间", "phase deadline reached")
		}
		if match := siloErrorPattern.FindStringSubmatch(result.Stderr); match != nil {
			message += " [" + match[1] + "]"
		}
		return "", errors.New(message)
	}
	return result.Stdout, nil
}

func siloPhaseBudget(phase string, buckets int) time.Duration {
	switch phase {
	case "state", "idle":
		return 10 * time.Second
	case "stage-dir", "clean-stage":
		return 30 * time.Second
	case "backup", "verify-backup":
		return 6 * time.Hour
	case "before":
		return 5*time.Minute + time.Duration(buckets)*3*time.Minute
	case "after":
		return 20*time.Minute + time.Duration(buckets)*3*time.Minute
	case "start", "start-old", "verify-old":
		return 12 * time.Minute
	default:
		return 5 * time.Minute
	}
}

func siloText(language, zh, en string) string {
	if normalizeLanguage(language) == "en" {
		return en
	}
	return zh
}
