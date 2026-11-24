package mysql

import (
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/taskrun"
)

const (
	OfflineUpgradeSourceVersion = "8.0.36"
	OfflineUpgradeTargetVersion = "8.4.13"
	offlineUpgradeToolsVersion  = "26.10.0"
)

//go:embed templates/innodb-cluster/offline-upgrade.sh
var offlineUpgradeScript string

func OfflineUpgradeClusterID(seed store.AppInstance) string { return clusterIDFromInstance(seed) }

var offlineUpgradeSteps = []struct{ name, en, zh string }{
	{"verify-cluster", "Verify bundle and three-member replication topology", "校验离线包和三成员复制拓扑"},
	{"stage-and-check", "Stage binaries and run MySQL upgrade checks", "暂存程序并执行 MySQL 升级检查"},
	{"cold-backups", "Create and verify old-version cold backups", "创建并校验旧版冷备份"},
	{"upgrade-tools", "Switch MySQL Shell and Router", "切换 MySQL Shell 和 Router"},
	{"upgrade-metadata", "Upgrade InnoDB Cluster metadata", "升级 InnoDB Cluster 元数据"},
	{"upgrade-secondaries", "Roll both MySQL secondaries", "滚动升级两台 MySQL 从节点"},
	{"upgrade-primary", "Roll the original MySQL primary", "滚动升级原 MySQL 主节点"},
	{"verify-and-record", "Verify members and routers and reconcile Panel versions", "复验成员和 Router 并更新面板版本"},
}

func PlanOfflineUpgrade(seed store.AppInstance, language string) []registry.InstallStepPlan {
	steps := make([]registry.InstallStepPlan, 0, len(offlineUpgradeSteps))
	for i, step := range offlineUpgradeSteps {
		steps = append(steps, registry.InstallStepPlan{Target: OfflineUpgradeClusterID(seed), Name: step.name, Title: upgradeText(language, step.en, step.zh), Order: i + 1})
	}
	return steps
}

func (m Module) PlanOfflineUpgrade(seed store.AppInstance, _ []store.AppInstance, language string) []registry.InstallStepPlan {
	return PlanOfflineUpgrade(seed, language)
}

func upgradeText(language, en, zh string) string {
	if strings.HasPrefix(strings.ToLower(language), "zh") {
		return zh
	}
	return en
}

type offlineUpgradeStore interface {
	maintenanceReader
	SaveAppInstancesIfUnchanged([]store.AppInstance) error
}

type upgradeNode struct {
	instance   store.AppInstance
	server     store.Server
	root       string
	work       string
	backup     string
	endpoint   string
	port       int
	credential store.Credential
}

type upgradeMember struct {
	uuid, endpoint, version, role, state string
}

type upgradeView struct {
	uuid, endpoint, group, version, gtid string
	members                              []upgradeMember
}

type upgradeProbeBlocker string

func (b upgradeProbeBlocker) Error() string { return string(b) }

type offlineUpgrade struct {
	service  Service
	data     offlineUpgradeStore
	run      registry.RunContext
	seed     store.AppInstance
	nodes    []upgradeNode
	routers  []upgradeNode
	bundle   Bundle
	primary  int
	baseline []upgradeView
	versions map[int]string
	verified map[string]store.AppInstance
	prepared []upgradeNode
	secrets  []string
}

func (m Module) RunOfflineUpgrade(ctx context.Context, seed store.AppInstance, resources []store.Resource, run registry.RunContext) error {
	return m.service.RunOfflineUpgrade(ctx, seed, resources, run)
}

func (s Service) RunOfflineUpgrade(ctx context.Context, seed store.AppInstance, resources []store.Resource, run registry.RunContext) (retErr error) {
	data, ok := s.store.(offlineUpgradeStore)
	if !ok || !validLogicalTaskID(run.TaskID) || OfflineUpgradeClusterID(seed) == "" {
		return errors.New(upgradeText(run.Language, "Invalid MySQL upgrade task or cluster", "MySQL 升级任务或集群无效"))
	}
	u := offlineUpgrade{service: s, data: data, run: run, seed: seed, versions: map[int]string{}, verified: map[string]store.AppInstance{}}
	target := OfflineUpgradeClusterID(seed)
	recorder, _ := run.Log.(taskrun.Recorder)
	taskrun.StartTarget(recorder, target)
	defer func() {
		retErr = errors.Join(retErr, u.cleanup(ctx))
		if retErr != nil {
			// Persist only the nodes whose target version and identity were confirmed.
			if err := u.saveVerified(); err != nil {
				retErr = errors.Join(retErr, err)
			}
			taskrun.FinishTarget(recorder, target, "failed", retErr.Error())
			return
		}
		taskrun.FinishTarget(recorder, target, "success", "")
	}()

	steps := PlanOfflineUpgrade(seed, run.Language)
	copy := CopyFor(run.Language)
	runner := taskrun.Runner{Log: run.Log, Recorder: recorder, Target: target,
		Messages: taskrun.Messages{StepStart: copy.StepStart, StepDone: copy.StepDone, StepFailed: copy.StepFailed}}
	for _, step := range steps {
		runner.Steps = append(runner.Steps, taskrun.Step{Name: step.Name, Title: step.Title})
	}
	actions := []func() error{
		func() error { return u.preflight(ctx, resources) },
		func() error { return u.stage(ctx) },
		func() error { return u.backup(ctx) },
		func() error { return u.upgradeTools(ctx) },
		func() error { return u.upgradeMetadata(ctx) },
		func() error { return u.upgradeSecondaries(ctx) },
		func() error { return u.upgradeServer(ctx, u.primary) },
		func() error { return u.verifyAndRecord(ctx) },
	}
	for i, step := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := runner.Run(step.Order, step.Name, step.Title, actions[i]); err != nil {
			return err
		}
	}
	return nil
}

var upgradePathPattern = regexp.MustCompile(`^/([A-Za-z0-9._-]+/)*[A-Za-z0-9._-]+$`)
var upgradeGTIDPattern = regexp.MustCompile(`^[0-9a-fA-F:,-]+$`)

func parseUpgradeView(output, endpoint string) (upgradeView, error) {
	view := upgradeView{endpoint: endpoint}
	invalid := errors.New("invalid MySQL upgrade replication view")
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		switch fields[0] {
		case "__AIFAR_UPGRADE_LOCAL__":
			// The final field verifies single-primary mode, safe boot, internal paths,
			// compatibility, no pending work, and baseline GTID inclusion.
			if len(fields) != 6 || view.uuid != "" || fields[5] != "1" ||
				!uuidPattern.MatchString(fields[1]) || !uuidPattern.MatchString(fields[2]) || !upgradeGTIDPattern.MatchString(fields[4]) {
				if len(fields) == 6 && slices.Contains([]string{"group-configuration", "storage-layout", "baseline-gtid", "native-password-accounts", "enabled-sql-events", "replication-queues"}, fields[5]) {
					return upgradeView{}, upgradeProbeBlocker(fields[5])
				}
				return upgradeView{}, invalid
			}
			view.uuid, view.group, view.version, view.gtid = fields[1], fields[2], fields[3], fields[4]
		case "__AIFAR_UPGRADE_MEMBER__":
			if len(fields) != 7 || !uuidPattern.MatchString(fields[1]) {
				return upgradeView{}, invalid
			}
			member := upgradeMember{uuid: fields[1], endpoint: net.JoinHostPort(fields[2], fields[3]), version: fields[4], role: fields[5], state: fields[6]}
			view.members = append(view.members, member)
		}
	}
	if view.uuid == "" || len(view.members) != 3 {
		return upgradeView{}, invalid
	}
	return view, nil
}

func validateUpgradeViews(views []upgradeView, versions map[int]string, baseline []string) (int, error) {
	invalid := errors.New("inconsistent MySQL upgrade replication views")
	if len(views) != 3 || (baseline != nil && len(baseline) != len(views)) {
		return -1, invalid
	}
	idx := make(map[string]int, len(views))
	for i, view := range views {
		if view.uuid == "" || view.group == "" || view.group != views[0].group || view.version != versions[i] ||
			(baseline != nil && baseline[i] != view.uuid) || len(view.members) != len(views) {
			return -1, invalid
		}
		if _, exists := idx[view.uuid]; exists {
			return -1, invalid
		}
		idx[view.uuid] = i
	}
	primary := -1
	for _, view := range views {
		seen := make(map[string]bool, len(views))
		localPrimary := -1
		for _, member := range view.members {
			index, known := idx[member.uuid]
			if !known || seen[member.uuid] || member.state != "ONLINE" || member.version != versions[index] ||
				normalizeEndpoint(member.endpoint) != normalizeEndpoint(views[index].endpoint) {
				return -1, invalid
			}
			switch member.role {
			case "PRIMARY":
				if localPrimary >= 0 {
					return -1, invalid
				}
				localPrimary = index
			case "SECONDARY":
			default:
				return -1, invalid
			}
			seen[member.uuid] = true
		}
		if localPrimary < 0 || (primary >= 0 && primary != localPrimary) {
			return -1, invalid
		}
		primary = localPrimary
	}
	return primary, nil
}

func upgradeCheckerPassed(output string) (passed, handledWriteSet bool) {
	var report struct {
		TargetVersion   string `json:"targetVersion"`
		ErrorCount      *int   `json:"errorCount"`
		ChecksPerformed []struct {
			ID               string `json:"id"`
			Status           string `json:"status"`
			DetectedProblems []struct {
				Level        string `json:"level"`
				DBObject     string `json:"dbObject"`
				DBObjectType string `json:"dbObjectType"`
				Description  string `json:"description"`
			} `json:"detectedProblems"`
		} `json:"checksPerformed"`
	}
	if json.Unmarshal([]byte(output), &report) != nil || report.TargetVersion != OfflineUpgradeTargetVersion ||
		report.ErrorCount == nil || len(report.ChecksPerformed) == 0 {
		return false, false
	}
	if *report.ErrorCount == 0 {
		return true, false
	}
	if *report.ErrorCount != 1 {
		return false, false
	}
	// The stage command removes this option from the candidate file and runs
	// mysqld --validate-config before the checker reads the live 8.0 server.
	for _, check := range report.ChecksPerformed {
		if check.Status != "OK" {
			return false, false
		}
		for _, problem := range check.DetectedProblems {
			if problem.Level == "Warning" || problem.Level == "Notice" {
				continue
			}
			if handledWriteSet || check.ID != "sysVars" || problem.Level != "Error" ||
				problem.DBObject != "binlog_transaction_dependency_tracking" || problem.DBObjectType != "SystemVariable" ||
				problem.Description != "variable removed at version 8.4.0 is set to WRITESET (EXPLICIT).." {
				return false, false
			}
			handledWriteSet = true
		}
	}
	return handledWriteSet, handledWriteSet
}

func (u *offlineUpgrade) preflight(ctx context.Context, resources []store.Resource) error {
	badCluster := errors.New(upgradeText(u.run.Language, "The recorded cluster and all three MySQL 8.0.36 members must be eligible for upgrade", "登记的集群和三台 MySQL 8.0.36 成员均须满足升级条件"))
	current, err := u.data.GetAppInstance(u.seed.ID)
	if err != nil || current.ServerID != u.seed.ServerID || OfflineUpgradeClusterID(current) != OfflineUpgradeClusterID(u.seed) {
		return badCluster
	}
	instances, err := maintenanceInstances(u.data, current)
	if err != nil || len(instances) != 3 || !containsInstance(instances, current.ID) {
		return badCluster
	}
	if err := u.service.requireNoMySQLMaintenance(current, u.run.Language); err != nil {
		return err
	}
	if err := u.service.requireNoMySQLReconciliation(current, u.run.Language); err != nil {
		return err
	}
	all, err := u.data.ListAppInstances()
	if err != nil {
		return badCluster
	}
	seenRouters := map[string]bool{}
	for _, instance := range all {
		if instance.App != "mysql-router" || OfflineUpgradeClusterID(instance) != OfflineUpgradeClusterID(current) {
			continue
		}
		if instance.Version != OfflineUpgradeSourceVersion || seenRouters[instance.ServerID] || instanceTopology(instance) != "router" {
			return badCluster
		}
		seenRouters[instance.ServerID] = true
		node, err := u.loadNode(instance, routerPort(instance))
		if err != nil {
			return err
		}
		u.routers = append(u.routers, node)
	}
	for i, instance := range instances {
		if instance.Version != OfflineUpgradeSourceVersion {
			return badCluster
		}
		node, err := u.loadNode(instance, instancePort(instance))
		if err != nil {
			return err
		}
		node.credential, err = resolveMySQLAdminCredential(u.data, instance)
		if err != nil {
			return localizedMySQLOperationError(u.run.Language, MySQLCredentialUnavailable)
		}
		u.secrets = append(u.secrets, node.credential.Secret["password"])
		u.nodes = append(u.nodes, node)
		u.versions[i] = OfflineUpgradeSourceVersion
	}
	var candidates []store.Resource
	for _, resource := range resources {
		if filepath.Base(resource.Path) == "mysql-aifar-8.4.13-official-bundle.tar" &&
			resource.App == "mysql" && resource.Part == "backend" && resource.Version == OfflineUpgradeTargetVersion {
			candidates = append(candidates, resource)
		}
	}
	u.bundle, err = SelectBundle(candidates, OfflineUpgradeTargetVersion)
	digest, digestErr := hex.DecodeString(u.bundle.ArchiveSHA256)
	if len(candidates) != 1 || err != nil || digestErr != nil || len(digest) != 32 || VerifyBundle(u.bundle) != nil {
		return errors.New(upgradeText(u.run.Language, "A verified offline MySQL 8.4.13 bundle with a SHA-256 digest is required", "需要带 SHA-256 且校验通过的 MySQL 8.4.13 离线包"))
	}
	return ctx.Err()
}

func (u *offlineUpgrade) loadNode(instance store.AppInstance, port int) (upgradeNode, error) {
	server, err := u.service.store.GetServer(instance.ServerID, true)
	deploy := installerkit.RemoteDeployDir(server.DeployDir)
	if err != nil || server.ID != instance.ServerID || port < 1 || port > 65535 || !upgradePathPattern.MatchString(deploy) || strings.TrimSpace(server.Host) == "" {
		return upgradeNode{}, errors.New(upgradeText(u.run.Language, "Invalid server, deployment directory or port", "服务器、部署目录或端口无效"))
	}
	return upgradeNode{
		instance: instance, server: server, root: path.Join(deploy, instance.App),
		work:     path.Join(deploy, "_work", "mysql-offline-upgrade-"+u.run.TaskID+"-"+instance.App),
		backup:   path.Join(deploy, "_backups", "mysql-offline-upgrade", u.run.TaskID, instance.App),
		endpoint: net.JoinHostPort(server.Host, strconv.Itoa(port)), port: port,
	}, nil
}

func (u *offlineUpgrade) stage(ctx context.Context) error {
	script, err := installerkit.WriteTempScript("aifar-mysql-offline-upgrade-*.sh", offlineUpgradeScript)
	if err != nil {
		return err
	}
	defer os.Remove(script)
	for _, node := range slices.Concat(u.nodes, u.routers) {
		parent := path.Dir(node.work)
		command := "set -eu; umask 077; install -d " + installerkit.ShellQuote(parent) + "; test \"$(realpath -e " + installerkit.ShellQuote(parent) + ")\" = " + installerkit.ShellQuote(parent) + "; mkdir -m 700 -- " + installerkit.ShellQuote(node.work)
		if _, err := u.remote(ctx, node, "prepare", command, false); err != nil {
			return err
		}
		u.prepared = append(u.prepared, node)
		if err := u.service.remote.UploadFile(ctx, node.server, script, path.Join(node.work, "upgrade.sh"), 0o600); err != nil {
			return u.phaseError(node, "upload-script")
		}
		if node.instance.App == "mysql" {
			if err := uploadMySQLCredentialContext(ctx, u.service.remote, node.server, node.credential, node.port, path.Join(node.work, "credential.cnf")); err != nil {
				return localizedMySQLOperationError(u.run.Language, MySQLCredentialUnavailable)
			}
		}
	}
	views, primary, err := u.views(ctx)
	if err != nil {
		return err
	}
	u.baseline = views
	for i, node := range slices.Concat(u.nodes, u.routers) {
		if err := u.service.remote.UploadFile(ctx, node.server, u.bundle.ArchivePath, path.Join(node.work, "bundle.tar"), 0o600); err != nil {
			return u.phaseError(node, "upload-bundle")
		}
		if _, err := u.script(ctx, node, "stage", u.bundle.ArchiveSHA256); err != nil {
			return err
		}
		if node.instance.App == "mysql-router" {
			continue
		}
		role := "SECONDARY"
		if i == primary {
			role = "PRIMARY"
		}
		output, err := u.script(ctx, node, "checker", role)
		passed, handledWriteSet := upgradeCheckerPassed(output)
		if err != nil || !passed {
			return u.phaseError(node, "upgrade-checker")
		}
		if handledWriteSet {
			log := u.run.LoggerForTarget(OfflineUpgradeClusterID(u.seed))
			log.Info(i18n.MiddlewareUpgradeText(u.run.Language, "mysqlWriteSetHandled"), node.endpoint)
		}
	}
	return nil
}

func (u *offlineUpgrade) backup(ctx context.Context) error {
	for _, node := range slices.Concat(u.nodes, u.routers) {
		if node.instance.App == "mysql" {
			if _, _, err := u.waitForViews(ctx); err != nil {
				return err
			}
		}
		if _, err := u.script(ctx, node, "backup"); err != nil {
			return err
		}
		mysqlSanitizedLogger{base: u.run.Log, secrets: u.secrets}.Info(upgradeText(u.run.Language,
			"Verified cold backup on %s at %s", "已校验 %s 上的冷备份 %s"), node.endpoint, node.backup)
	}
	_, _, err := u.waitForViews(ctx)
	return err
}

func (u *offlineUpgrade) upgradeTools(ctx context.Context) error {
	for _, node := range slices.Concat(u.nodes, u.routers) {
		if _, err := u.script(ctx, node, "tools"); err != nil {
			return err
		}
	}
	_, primary, err := u.waitForViews(ctx)
	if err != nil {
		return err
	}
	if err := u.verifyRouters(ctx, primary); err != nil {
		return err
	}
	for _, router := range u.routers {
		if err := u.markVerified(router, true); err != nil {
			return err
		}
	}
	return nil
}

func (u *offlineUpgrade) upgradeMetadata(ctx context.Context) error {
	_, primary, err := u.waitForViews(ctx)
	if err != nil {
		return err
	}
	if _, err := u.script(ctx, u.nodes[primary], "metadata-dry-run"); err != nil {
		return err
	}
	if _, err := u.script(ctx, u.nodes[primary], "metadata-upgrade"); err != nil {
		return err
	}
	_, primary, err = u.waitForViews(ctx)
	if err != nil {
		return err
	}
	return u.verifyRouters(ctx, primary)
}

func (u *offlineUpgrade) upgradeSecondaries(ctx context.Context) error {
	_, primary, err := u.waitForViews(ctx)
	if err != nil {
		return err
	}
	u.primary = primary
	for i := range u.nodes {
		if i != primary {
			if err := u.upgradeServer(ctx, i); err != nil {
				return err
			}
		}
	}
	return nil
}

func (u *offlineUpgrade) upgradeServer(ctx context.Context, index int) error {
	_, primary, err := u.waitForViews(ctx)
	node := u.nodes[index]
	if err != nil {
		return err
	}
	if primary != u.primary || u.versions[index] != OfflineUpgradeSourceVersion {
		return u.phaseError(node, "primary-changed")
	}
	if _, err := u.script(ctx, node, "server", u.baseline[index].uuid); err != nil {
		return err
	}
	u.versions[index] = OfflineUpgradeTargetVersion
	if _, _, err := u.waitForViews(ctx); err != nil {
		return err
	}
	return u.markVerified(node, false)
}

func (u *offlineUpgrade) verifyAndRecord(ctx context.Context) error {
	_, primary, err := u.waitForViews(ctx)
	if err != nil {
		return err
	}
	if err := u.verifyRouters(ctx, primary); err != nil {
		return err
	}
	return u.saveVerified()
}

func (u *offlineUpgrade) verifyRouters(ctx context.Context, primary int) error {
	for _, router := range u.routers {
		if _, err := u.script(ctx, router, "verify-router"); err != nil {
			return err
		}
		if _, err := u.script(ctx, u.nodes[primary], "route-sql", router.server.Host, strconv.Itoa(router.port), u.baseline[primary].uuid); err != nil {
			return u.phaseError(router, "verify-router-rw")
		}
	}
	return nil
}

func (u *offlineUpgrade) markVerified(node upgradeNode, router bool) error {
	item := node.instance
	metadata := appMetadata(item)
	metadata["resourcePath"] = u.bundle.ArchivePath
	metadata["offlineUpgradeBackupPath"] = node.backup
	metadata["offlineUpgradeTaskId"] = u.run.TaskID
	if router {
		metadata["routerVersion"] = offlineUpgradeToolsVersion
	} else {
		metadata["mysqlShellVersion"] = offlineUpgradeToolsVersion
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	item.Metadata, item.Version = string(encoded), OfflineUpgradeTargetVersion
	u.verified[item.ID] = item
	return nil
}

func (u *offlineUpgrade) saveVerified() error {
	if len(u.verified) == 0 {
		return nil
	}
	items := make([]store.AppInstance, 0, len(u.verified))
	for _, item := range u.verified {
		items = append(items, item)
	}
	if err := u.data.SaveAppInstancesIfUnchanged(items); err != nil {
		return errors.New(upgradeText(u.run.Language, "Could not record verified MySQL versions because Panel instance records changed or could not be written", "面板实例记录已变化或写入失败，无法登记已校验的 MySQL 版本"))
	}
	clear(u.verified)
	return nil
}

func (u *offlineUpgrade) waitForViews(ctx context.Context) ([]upgradeView, int, error) {
	deadline, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	for {
		views, primary, err := u.views(deadline)
		if err == nil {
			return views, primary, nil
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-deadline.Done():
			timer.Stop()
			return nil, -1, errors.Join(err, deadline.Err())
		case <-timer.C:
		}
	}
}

func (u *offlineUpgrade) views(ctx context.Context) ([]upgradeView, int, error) {
	var views []upgradeView
	var known []string
	for i, node := range u.nodes {
		baseline := ""
		if u.baseline != nil {
			baseline = u.baseline[i].gtid
			known = append(known, u.baseline[i].uuid)
		}
		output, err := u.script(ctx, node, "probe", baseline)
		if err != nil {
			return nil, -1, err
		}
		view, err := parseUpgradeView(output, node.endpoint)
		if err != nil || (u.baseline != nil && view.group != u.baseline[i].group) {
			var blocker upgradeProbeBlocker
			if errors.As(err, &blocker) {
				return nil, -1, u.phaseError(node, string(blocker))
			}
			return nil, -1, u.phaseError(node, "replication-view")
		}
		views = append(views, view)
	}
	primary, err := validateUpgradeViews(views, u.versions, known)
	if err != nil {
		return nil, -1, errors.New(upgradeText(u.run.Language, "The three direct MySQL observations disagree on members, roles, versions, or recovery state", "三台 MySQL 直连视图的成员、角色、版本或恢复状态不一致"))
	}
	return views, primary, nil
}

func (u *offlineUpgrade) phaseError(node upgradeNode, phase string) error {
	return fmt.Errorf(upgradeText(u.run.Language, "MySQL upgrade stopped at %s on %s. Check the task log and preserve the backup directory %s", "MySQL 升级在 %s 阶段、节点 %s 停止。请检查任务日志并保留备份目录 %s"), phase, node.endpoint, node.backup)
}

func (u *offlineUpgrade) script(ctx context.Context, node upgradeNode, phase string, args ...string) (string, error) {
	params := []string{path.Join(node.work, "upgrade.sh"), phase, node.instance.App, node.root, node.work, node.backup, u.run.TaskID, strconv.Itoa(node.port)}
	params = append(params, args...)
	for i := range params {
		params[i] = installerkit.ShellQuote(params[i])
	}
	return u.remote(ctx, node, phase, "bash "+strings.Join(params, " "), phase == "probe" || phase == "route-sql")
}

func (u *offlineUpgrade) remote(ctx context.Context, node upgradeNode, phase, command string, protocolOutput bool) (string, error) {
	result, err := u.service.remote.Run(ctx, node.server, command)
	log := mysqlSanitizedLogger{base: u.run.LoggerForTarget(OfflineUpgradeClusterID(u.seed)), secrets: u.secrets}
	if !protocolOutput || err != nil {
		installerkit.LogCommandResult(result, err, log)
	}
	if err != nil || result.ExitCode != 0 {
		return "", u.phaseError(node, phase)
	}
	return result.Stdout, nil
}

func (u *offlineUpgrade) cleanup(ctx context.Context) (result error) {
	for _, node := range u.prepared {
		cleanupCtx, cancel := mysqlCredentialCleanupContext(ctx)
		// Keep reports, staged trees, and backups. The archive and CNF were uploads for this task only.
		command := "rm -f -- " + installerkit.ShellQuote(path.Join(node.work, "credential.cnf")) + " " + installerkit.ShellQuote(path.Join(node.work, "bundle.tar"))
		if output, err := u.service.remote.Run(cleanupCtx, node.server, command); err != nil || output.ExitCode != 0 {
			result = errors.Join(result, u.phaseError(node, "cleanup-credentials"))
			mysqlSanitizedLogger{base: u.run.Log, secrets: u.secrets}.Error(upgradeText(u.run.Language,
				"Could not clean the MySQL credential file or upload on %s at %s", "无法清理 %s 上的 MySQL 凭据文件或上传文件 %s"), node.endpoint, node.work)
		}
		cancel()
	}
	return result
}
