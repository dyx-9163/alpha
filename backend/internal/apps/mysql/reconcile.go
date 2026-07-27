package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

type localInfileSessionFactory func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error)

func defaultLocalInfileSessionFactory(remote Remote) localInfileSessionFactory {
	return func(ctx context.Context, instance store.AppInstance, server store.Server, credential store.Credential) (localInfileSession, func(), error) {
		if remote == nil {
			return nil, func() {}, errors.New("local_infile session is unavailable")
		}
		work := mysqlBackupWorkDir(store.NewID("reconcile"))
		if _, err := remote.Run(ctx, server, bootstrapBackupWorkCommand(work)); err != nil {
			return nil, func() {}, err
		}
		cleanup := func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = remote.Run(cleanupCtx, server, cleanupBackupCommand(work))
		}
		secretPath, err := writeMySQLSecretContext(credential, instancePort(instance))
		if err != nil {
			cleanup()
			return nil, func() {}, mysqlOperationError(MySQLCredentialUnavailable)
		}
		defer os.Remove(secretPath)
		if err := remote.UploadFile(ctx, server, secretPath, path.Join(work, "secret-context.cnf"), 0o600); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		return &remoteLocalInfileSession{remote: remote, server: server, work: work, port: instancePort(instance)}, cleanup, nil
	}
}

type mysqlReconciliationMarker struct {
	Version       int    `json:"version"`
	Kind          string `json:"kind"`
	OriginalValue string `json:"originalValue"`
	RecordedAt    string `json:"recordedAt"`
	TaskID        string `json:"taskId"`
}

type remoteLocalInfileSession struct {
	remote Remote
	server store.Server
	work   string
	port   int
}

func (s *remoteLocalInfileSession) ReadLocalInfile(ctx context.Context) (string, error) {
	result, err := s.remote.Run(ctx, s.server, localInfileReadCommand(s.work, s.port))
	if err != nil {
		return "", err
	}
	value := strings.ToUpper(strings.TrimSpace(result.Stdout))
	if value != "ON" && value != "OFF" {
		return "", errors.New("local_infile returned an unsupported value")
	}
	return value, nil
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
	return localInfileSQLCommand(work, port, "SELECT @@GLOBAL.local_infile")
}

func localInfileSetCommand(work string, port int, value string) string {
	if value != "ON" && value != "OFF" {
		return "false"
	}
	return localInfileSQLCommand(work, port, "SET GLOBAL local_infile = "+value)
}

func localInfileSQLCommand(work string, port int, query string) string {
	mysqlsh := path.Join(mysqlInstallRoot, "mysql-shell", "bin", "mysqlsh")
	return "set -eu; test -x " + installerkit.ShellQuote(mysqlsh) + "; " + installerkit.ShellQuote(mysqlsh) +
		" --defaults-file=" + installerkit.ShellQuote(path.Join(work, "secret-context.cnf")) +
		" --sql --raw --skip-column-names --host=127.0.0.1 --port=" + strconv.Itoa(port) +
		" --execute " + installerkit.ShellQuote(query)
}

func (s Service) reconcileMySQL(ctx context.Context, expected store.AppInstance, language string) error {
	data, ok := s.store.(restoreStore)
	if !ok {
		_, _, present, err := parseMySQLReconciliationMarker(expected.Metadata)
		if err != nil || present {
			return localizedMySQLOperationError(language, MySQLReconciliationRequired)
		}
		return nil
	}
	instance, err := data.GetAppInstance(expected.ID)
	if err != nil {
		return err
	}
	_, marker, present, err := parseMySQLReconciliationMarker(instance.Metadata)
	if err != nil {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	if !present {
		return nil
	}
	server, err := s.store.GetServer(instance.ServerID, false)
	if err != nil {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	credential, err := data.GetBoundCredential(instance.ID, "admin", true)
	if err != nil || credential.Status != "active" || credential.Kind != "mysql" || strings.TrimSpace(credential.Secret["password"]) == "" {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	session, cleanup, err := s.localInfileSession(ctx, instance, server, credential)
	if err != nil {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	defer cleanup()
	if err := session.SetLocalInfile(ctx, marker.OriginalValue); err != nil {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	value, err := session.ReadLocalInfile(ctx)
	if err != nil || strings.ToUpper(strings.TrimSpace(value)) != marker.OriginalValue {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	if err := clearMySQLReconciliationMarker(data, instance.ID, marker.OriginalValue, marker.TaskID); err != nil {
		return localizedMySQLOperationError(language, MySQLReconciliationRequired)
	}
	return nil
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
