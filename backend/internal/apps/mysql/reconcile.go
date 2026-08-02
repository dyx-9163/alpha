package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

type localInfileSessionFactory func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error)

type reconciliationSessionFactory func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func() error, error)

func defaultReconciliationSessionFactory(remote Remote) reconciliationSessionFactory {
	return reconciliationSessionFactoryWithRemove(remote, os.Remove)
}

func reconciliationSessionFactoryWithRemove(remote Remote, removeSecret func(string) error) reconciliationSessionFactory {
	return func(ctx context.Context, instance store.AppInstance, server store.Server, credential store.Credential) (localInfileSession, func() error, error) {
		if remote == nil {
			return nil, func() error { return nil }, errors.New("reconciliation session is unavailable")
		}
		work := mysqlBackupWorkDir(store.NewID("reconcile"))
		secretPath := ""
		cleanup := func() error {
			var cleanupErrors []error
			if secretPath != "" {
				if err := removeSecret(secretPath); err != nil && !errors.Is(err, os.ErrNotExist) {
					cleanupErrors = append(cleanupErrors, errors.New("local reconciliation secret cleanup failed"))
				}
			}
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := remote.Run(cleanupCtx, server, cleanupBackupCommand(work)); err != nil {
				cleanupErrors = append(cleanupErrors, errors.New("remote reconciliation secret cleanup failed"))
			}
			return errors.Join(cleanupErrors...)
		}
		failSetup := func() (localInfileSession, func() error, error) {
			cleanupErr := cleanup()
			return nil, func() error { return nil }, errors.Join(errors.New("reconciliation session setup failed"), cleanupErr)
		}
		if _, err := remote.Run(ctx, server, bootstrapBackupWorkCommand(work)); err != nil {
			return failSetup()
		}
		var err error
		secretPath, err = writeMySQLSecretContext(credential, instancePort(instance))
		if err != nil {
			return failSetup()
		}
		if err := remote.UploadFile(ctx, server, secretPath, path.Join(work, "secret-context.cnf"), 0o600); err != nil {
			return failSetup()
		}
		return &remoteLocalInfileSession{remote: remote, server: server, work: work, port: instancePort(instance)}, cleanup, nil
	}
}

func defaultLocalInfileSessionFactory(remote Remote) localInfileSessionFactory {
	return func(ctx context.Context, instance store.AppInstance, server store.Server, credential store.Credential) (localInfileSession, func(), error) {
		if remote == nil {
			return nil, func() {}, errors.New("local_infile session is unavailable")
		}
		work := mysqlBackupWorkDir(store.NewID("reconcile"))
		if _, err := remote.Run(ctx, server, bootstrapBackupWorkCommand(work)); err != nil {
			return nil, func() {}, err
		}
		var session *remoteLocalInfileSession
		var secretPath string
		cleanupNow := func() error {
			var cleanupErrors []error
			if secretPath != "" {
				if err := removeMySQLCredentialContext(secretPath); err != nil {
					cleanupErrors = append(cleanupErrors, err)
				}
			}
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := remote.Run(cleanupCtx, server, cleanupBackupCommand(work)); err != nil {
				cleanupErrors = append(cleanupErrors, errors.New("unable to clean remote MySQL credential context"))
			}
			return errors.Join(cleanupErrors...)
		}
		cleanup := func() {
			cleanupErr := cleanupNow()
			if session != nil {
				session.credentialCleanupErr = cleanupErr
			}
		}
		var err error
		secretPath, err = writeMySQLSecretContext(credential, instancePort(instance))
		if err != nil {
			return nil, func() {}, errors.Join(mysqlOperationError(MySQLCredentialUnavailable), cleanupNow())
		}
		if err := remote.UploadFile(ctx, server, secretPath, path.Join(work, "secret-context.cnf"), 0o600); err != nil {
			return nil, func() {}, errors.Join(err, cleanupNow())
		}
		session = &remoteLocalInfileSession{remote: remote, server: server, work: work, port: instancePort(instance)}
		return session, cleanup, nil
	}
}

type mysqlReconciliationMarker struct {
	Version       int    `json:"version"`
	Kind          string `json:"kind"`
	OriginalValue string `json:"originalValue"`
	RecordedAt    string `json:"recordedAt"`
	TaskID        string `json:"taskId"`
}

// ReconciliationMemberIdentity is an immutable plan-time binding between an
// authoritative cluster-member row, app instance, and server.
type ReconciliationMemberIdentity struct {
	MemberID   string
	InstanceID string
	ServerID   string
}

// ReconciliationPlan is built from persisted control-plane state before task
// creation and revalidated after the raw mutation lock is acquired.
type ReconciliationPlan struct {
	Instance store.AppInstance
	Cluster  store.AppCluster
	Members  []ReconciliationMemberIdentity
}

func BuildReconciliationPlan(data maintenanceReader, expected store.AppInstance) (ReconciliationPlan, error) {
	fresh, err := data.GetAppInstance(expected.ID)
	if err != nil || fresh.App != "mysql" || fresh.ServerID != expected.ServerID || instanceTopology(fresh) != instanceTopology(expected) || clusterIDFromInstance(fresh) != clusterIDFromInstance(expected) {
		return ReconciliationPlan{}, errors.New("invalid reconciliation plan instance")
	}
	instances, err := maintenanceInstances(data, fresh)
	if err != nil || !containsInstance(instances, fresh.ID) {
		return ReconciliationPlan{}, errors.New("invalid reconciliation plan topology")
	}
	plan := ReconciliationPlan{Instance: fresh}
	if instanceTopology(fresh) == "standalone" {
		plan.Members = []ReconciliationMemberIdentity{{InstanceID: fresh.ID, ServerID: fresh.ServerID}}
		return plan, nil
	}
	clusterID := clusterIDFromInstance(fresh)
	plan.Cluster, err = data.GetAppCluster(clusterID)
	if err != nil {
		return ReconciliationPlan{}, errors.New("invalid reconciliation plan cluster")
	}
	members, err := data.ListAppClusterMembers(clusterID)
	if err != nil || len(members) != 3 {
		return ReconciliationPlan{}, errors.New("invalid reconciliation plan members")
	}
	plan.Members = make([]ReconciliationMemberIdentity, 0, len(members))
	for _, member := range members {
		plan.Members = append(plan.Members, ReconciliationMemberIdentity{MemberID: member.ID, InstanceID: member.InstanceID, ServerID: member.ServerID})
	}
	sort.Slice(plan.Members, func(i, j int) bool { return plan.Members[i].InstanceID < plan.Members[j].InstanceID })
	return plan, nil
}

func sameReconciliationPlan(planned, current ReconciliationPlan) bool {
	if planned.Instance.ID != current.Instance.ID || planned.Instance.ServerID != current.Instance.ServerID ||
		instanceTopology(planned.Instance) != instanceTopology(current.Instance) ||
		clusterIDFromInstance(planned.Instance) != clusterIDFromInstance(current.Instance) || planned.Cluster != current.Cluster ||
		len(planned.Members) != len(current.Members) {
		return false
	}
	for index := range planned.Members {
		if planned.Members[index] != current.Members[index] {
			return false
		}
	}
	return true
}

type reconciliationStore interface {
	backupStore
	ClearMySQLReconciliation(instanceID, originalValue, recordedAt, taskID string) error
}

type remoteLocalInfileSession struct {
	remote               Remote
	server               store.Server
	work                 string
	port                 int
	credentialCleanupErr error
}

func (s *remoteLocalInfileSession) CredentialCleanupError() error { return s.credentialCleanupErr }

type credentialCleanupReporter interface {
	CredentialCleanupError() error
}

func (s *remoteLocalInfileSession) ReadLocalInfile(ctx context.Context) (string, error) {
	result, err := s.remote.Run(ctx, s.server, localInfileReadCommand(s.work, s.port))
	if err != nil {
		return "", err
	}
	return parseLocalInfileOutput(result.Stdout)
}

func parseLocalInfileOutput(output string) (string, error) {
	if value, ok := normalizeLocalInfileValue(output); ok {
		return value, nil
	}
	for _, line := range strings.Split(output, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "\t")
		if len(parts) == 2 && parts[1] == "__AIFAR_LOCAL_INFILE__" {
			if value, ok := normalizeLocalInfileValue(parts[0]); ok {
				return value, nil
			}
		}
	}
	return "", errors.New("local_infile returned an unsupported value")
}

func normalizeLocalInfileValue(raw string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "ON", "1":
		return "ON", true
	case "OFF", "0":
		return "OFF", true
	default:
		return "", false
	}
}

func (s *remoteLocalInfileSession) SetLocalInfile(ctx context.Context, value string) error {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value != "ON" && value != "OFF" {
		return errors.New("local_infile value is invalid")
	}
	_, err := s.remote.Run(ctx, s.server, localInfileSetCommand(s.work, s.port, value))
	return err
}

func localInfileReadCommand(work string, port int) string {
	return localInfileSQLCommand(work, port, "SELECT @@GLOBAL.local_infile AS value, '__AIFAR_LOCAL_INFILE__' AS marker")
}

func localInfileSetCommand(work string, port int, value string) string {
	if value != "ON" && value != "OFF" {
		return "false"
	}
	return localInfileSQLCommand(work, port, "SET GLOBAL local_infile = "+value)
}

func localInfileSQLCommand(work string, port int, query string) string {
	mysqlsh := path.Join(mysqlInstallRoot, "mysql-shell", "bin", "mysqlsh")
	secretPath := path.Join(work, "secret-context.cnf")
	return mysqlRemoteCredentialValidationCommand(secretPath) + "; test -x " + installerkit.ShellQuote(mysqlsh) + "; " + installerkit.ShellQuote(mysqlsh) +
		" --defaults-file=" + installerkit.ShellQuote(secretPath) +
		" --sql --result-format=tabbed --host=127.0.0.1 --port=" + strconv.Itoa(port) +
		" --execute " + installerkit.ShellQuote(query)
}

func (s Service) requireNoMySQLReconciliation(expected store.AppInstance, language string) error {
	data, ok := s.store.(backupStore)
	if !ok {
		_, _, present, err := parseMySQLReconciliationMarker(expected.Metadata)
		if err != nil || present {
			return localizedMySQLOperationError(language, MySQLReconciliationRequired)
		}
		return nil
	}
	fresh, err := data.GetAppInstance(expected.ID)
	if err != nil || fresh.App != "mysql" || fresh.ServerID != expected.ServerID || instanceTopology(fresh) != instanceTopology(expected) || clusterIDFromInstance(fresh) != clusterIDFromInstance(expected) {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	_, _, representativePresent, representativeErr := parseMySQLReconciliationMarker(fresh.Metadata)
	if representativeErr != nil || representativePresent {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	instances := []store.AppInstance{fresh}
	if instanceTopology(fresh) == "innodb-cluster" {
		topology, supported := s.store.(maintenanceReader)
		if !supported {
			return nil
		}
		instances, err = maintenanceInstances(topology, fresh)
		if err != nil || !containsInstance(instances, fresh.ID) {
			// Invalid cluster ownership is handled by the lifecycle resolver's
			// cluster-health contract. With no representative marker present,
			// it is not itself evidence of unfinished reconciliation.
			return nil
		}
	} else if instanceTopology(fresh) != "standalone" {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	for _, instance := range instances {
		if instance.ID == fresh.ID {
			continue
		}
		_, _, present, parseErr := parseMySQLReconciliationMarker(instance.Metadata)
		if parseErr != nil || present {
			return localizedMySQLOperationError(language, MySQLReconciliationRequired)
		}
	}
	return nil
}

// ReconciliationMarkerState is the fail-closed public parser used by the
// HTTP boundary. It exposes no marker contents or credential material.
func ReconciliationMarkerState(metadata string) (bool, error) {
	_, _, present, err := parseMySQLReconciliationMarker(metadata)
	return present, err
}

// Reconcile is the only public lifecycle entry allowed to mutate a persisted
// local_infile reconciliation marker.
func (m Module) Reconcile(ctx context.Context, plan ReconciliationPlan, language, _ string, _ Logger) error {
	data, ok := m.service.store.(interface {
		reconciliationStore
		maintenanceStore
	})
	if !ok {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	currentPlan, err := BuildReconciliationPlan(data, plan.Instance)
	if err != nil || !sameReconciliationPlan(plan, currentPlan) {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	fresh := currentPlan.Instance
	initialMembers, err := maintenanceInstances(data, fresh)
	if err != nil || !containsInstance(initialMembers, fresh.ID) || !validOptionalMaintenanceState(initialMembers) {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	_, marker, present, err := parseMySQLReconciliationMarker(fresh.Metadata)
	if err != nil {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	if !present {
		return localizedMySQLOperationError(language, MySQLReconciliationNotRequired)
	}
	_, expectedMarker, expectedPresent, expectedErr := parseMySQLReconciliationMarker(plan.Instance.Metadata)
	if expectedErr != nil || !expectedPresent || expectedMarker != marker {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	server, err := m.service.store.GetServer(fresh.ServerID, true)
	if err != nil {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	credential, err := data.GetBoundCredential(fresh.ID, "admin", true)
	if err != nil || credential.Status != "active" || credential.Kind != "mysql" ||
		strings.TrimSpace(credential.Username) == "" || strings.TrimSpace(credential.Secret["password"]) == "" {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	factory := m.service.reconciliationSession
	if factory == nil {
		factory = defaultReconciliationSessionFactory(m.service.remote)
	}
	session, cleanup, err := factory(ctx, fresh, server, credential)
	if err != nil {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	var primaryErr error
	if err := session.SetLocalInfile(ctx, marker.OriginalValue); err != nil {
		primaryErr = errors.New("reconciliation mutation failed")
	}
	if primaryErr == nil {
		value, readErr := session.ReadLocalInfile(ctx)
		if readErr != nil || strings.ToUpper(strings.TrimSpace(value)) != marker.OriginalValue {
			primaryErr = errors.New("reconciliation readback failed")
		}
	}
	var latest store.AppInstance
	if primaryErr == nil {
		latest, err = data.GetAppInstance(fresh.ID)
		if err != nil || latest.App != "mysql" || latest.ServerID != fresh.ServerID || instanceTopology(latest) != instanceTopology(fresh) || clusterIDFromInstance(latest) != clusterIDFromInstance(fresh) {
			primaryErr = errors.New("reconciliation topology changed")
		}
	}
	if primaryErr == nil {
		latestMembers, membersErr := maintenanceInstances(data, latest)
		if membersErr != nil || !sameInstanceIDs(initialMembers, latestMembers) {
			primaryErr = errors.New("reconciliation membership changed")
		}
	}
	if primaryErr == nil {
		_, latestMarker, latestPresent, markerErr := parseMySQLReconciliationMarker(latest.Metadata)
		if markerErr != nil || !latestPresent || latestMarker != marker {
			primaryErr = errors.New("reconciliation marker changed")
		}
	}
	cleanupErr := cleanup()
	if primaryErr != nil || cleanupErr != nil {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	if err := data.ClearMySQLReconciliation(latest.ID, marker.OriginalValue, marker.RecordedAt, marker.TaskID); err != nil {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	return nil
}

func validOptionalMaintenanceState(instances []store.AppInstance) bool {
	presentCount := 0
	var common store.MySQLMaintenanceMarker
	for _, instance := range instances {
		marker, present, err := store.ParseMySQLMaintenanceMarker(instance.Metadata)
		if err != nil {
			return false
		}
		if present {
			if presentCount > 0 && !sameMaintenanceMarker(common, marker) {
				return false
			}
			common = marker
			presentCount++
		}
	}
	if presentCount == 0 {
		return true
	}
	if presentCount != len(instances) {
		return false
	}
	if len(instances) == 1 {
		return common.Scope == "standalone" && instanceTopology(instances[0]) == "standalone"
	}
	return len(instances) == 3 && common.Scope == "cluster" && common.ClusterID == clusterIDFromInstance(instances[0])
}

func containsInstance(instances []store.AppInstance, id string) bool {
	for _, instance := range instances {
		if instance.ID == id {
			return true
		}
	}
	return false
}

func sameInstanceIDs(left, right []store.AppInstance) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID || left[index].ServerID != right[index].ServerID {
			return false
		}
	}
	return true
}

func (s Service) restoreMySQLReconciliation(ctx context.Context, expected store.AppInstance, language string) (mysqlReconciliationMarker, bool, error) {
	data, ok := s.store.(restoreStore)
	if !ok {
		_, _, present, err := parseMySQLReconciliationMarker(expected.Metadata)
		if err != nil || present {
			return mysqlReconciliationMarker{}, present, localizedMySQLOperationError(language, MySQLReconciliationRequired)
		}
		return mysqlReconciliationMarker{}, false, nil
	}
	instance, err := data.GetAppInstance(expected.ID)
	if err != nil {
		return mysqlReconciliationMarker{}, false, err
	}
	_, marker, present, err := parseMySQLReconciliationMarker(instance.Metadata)
	if err != nil {
		return mysqlReconciliationMarker{}, present, localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	if !present {
		return mysqlReconciliationMarker{}, false, nil
	}
	server, err := s.store.GetServer(instance.ServerID, true)
	if err != nil {
		return mysqlReconciliationMarker{}, true, localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	credential, err := data.GetBoundCredential(instance.ID, "admin", true)
	if err != nil || credential.Status != "active" || credential.Kind != "mysql" || strings.TrimSpace(credential.Secret["password"]) == "" {
		return mysqlReconciliationMarker{}, true, localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	factory := s.reconciliationSession
	if factory == nil {
		factory = defaultReconciliationSessionFactory(s.remote)
	}
	session, cleanup, err := factory(ctx, instance, server, credential)
	if err != nil {
		return mysqlReconciliationMarker{}, true, localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	var primaryErr error
	if err := session.SetLocalInfile(ctx, marker.OriginalValue); err != nil {
		primaryErr = errors.New("MySQL reconciliation mutation failed")
	}
	if primaryErr == nil {
		value, readErr := session.ReadLocalInfile(ctx)
		if readErr != nil || strings.ToUpper(strings.TrimSpace(value)) != marker.OriginalValue {
			primaryErr = errors.New("MySQL reconciliation readback failed")
		}
	}
	cleanupErr := cleanup()
	if primaryErr != nil || cleanupErr != nil {
		return mysqlReconciliationMarker{}, true, localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	return marker, true, nil
}

func parseMySQLReconciliationMarker(raw string) (map[string]json.RawMessage, mysqlReconciliationMarker, bool, error) {
	metadata, err := strictBackupMetadata(raw)
	if err != nil {
		return nil, mysqlReconciliationMarker{}, false, err
	}
	rawMarker, present := metadata["mysqlReconciliation"]
	if !present {
		return metadata, mysqlReconciliationMarker{}, false, nil
	}
	var marker mysqlReconciliationMarker
	decoder := json.NewDecoder(strings.NewReader(string(rawMarker)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&marker); err != nil {
		return nil, mysqlReconciliationMarker{}, true, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, mysqlReconciliationMarker{}, true, errors.New("invalid MySQL reconciliation marker")
	}
	parsedAt, parseErr := time.Parse(time.RFC3339, marker.RecordedAt)
	if marker.Version != 1 || marker.Kind != "local_infile" ||
		(marker.OriginalValue != "ON" && marker.OriginalValue != "OFF") ||
		parseErr != nil || parsedAt.Location() != time.UTC || !validLogicalTaskID(marker.TaskID) {
		return nil, mysqlReconciliationMarker{}, true, errors.New("invalid MySQL reconciliation marker")
	}
	return metadata, marker, true, nil
}

type localInfileSession interface {
	ReadLocalInfile(context.Context) (string, error)
	SetLocalInfile(context.Context, string) error
}

type localInfileGuard struct {
	session  localInfileSession
	original string
	captured bool
	restored bool
}

func newLocalInfileGuard(session localInfileSession) *localInfileGuard {
	return &localInfileGuard{session: session}
}

func (g *localInfileGuard) Capture(ctx context.Context) error {
	if g == nil || g.session == nil {
		return errors.New("local_infile session is unavailable")
	}
	value, err := g.session.ReadLocalInfile(ctx)
	if err != nil {
		return err
	}
	value = strings.ToUpper(strings.TrimSpace(value))
	if value != "ON" && value != "OFF" {
		return errors.New("local_infile returned an unsupported value")
	}
	g.original = value
	g.captured = true
	g.restored = false
	return nil
}

func (g *localInfileGuard) Enable(ctx context.Context) error {
	if g == nil || !g.captured {
		return errors.New("local_infile was not captured")
	}
	return g.session.SetLocalInfile(ctx, "ON")
}

func (g *localInfileGuard) Restore(ctx context.Context) error {
	if g == nil || !g.captured {
		return errors.New("local_infile was not captured")
	}
	if g.restored {
		return nil
	}
	value, err := g.session.ReadLocalInfile(ctx)
	if err != nil {
		return err
	}
	if strings.ToUpper(strings.TrimSpace(value)) != g.original {
		if err := g.session.SetLocalInfile(ctx, g.original); err != nil {
			return err
		}
		value, err = g.session.ReadLocalInfile(ctx)
		if err != nil {
			return err
		}
		if strings.ToUpper(strings.TrimSpace(value)) != g.original {
			return errors.New("local_infile original value was not restored")
		}
	}
	g.restored = true
	return nil
}
