package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/logmask"
	"aifar-deployment/backend/internal/store"
)

const mysqlCredentialContextVersion = 1

var removeMySQLCredentialContextFile = os.Remove

type mysqlCredentialResolver interface {
	GetBoundCredential(appInstanceID, purpose string, includeSecret bool) (store.Credential, error)
}

type mysqlConnectionCredential struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
}

type mysqlJSONCredentialContext struct {
	Version     int                         `json:"version"`
	Connections []mysqlConnectionCredential `json:"connections"`
}

func resolveMySQLAdminCredential(data any, instance store.AppInstance) (store.Credential, error) {
	credentials, ok := data.(mysqlCredentialResolver)
	if !ok {
		return store.Credential{}, mysqlOperationError(MySQLCredentialUnavailable)
	}
	credential, err := credentials.GetBoundCredential(instance.ID, "admin", true)
	if err != nil || credential.Status != "active" || credential.Kind != "mysql" ||
		strings.TrimSpace(credential.Username) == "" || strings.TrimSpace(credential.Secret["password"]) == "" {
		return store.Credential{}, mysqlOperationError(MySQLCredentialUnavailable)
	}
	return credential, nil
}

func writeMySQLInstallCredentialContext(username, password string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" || strings.ContainsAny(username, "\x00\r\n") || strings.ContainsAny(password, "\x00\r\n") {
		return "", errors.New("invalid MySQL install credential")
	}
	return writeMySQLCredentialContextBytes([]byte("AIFAR_MYSQL_CREDENTIAL_CONTEXT_V1\n" + username + "\n" + password + "\n"))
}

func writeMySQLJSONCredentialContext(connections []mysqlConnectionCredential) (string, error) {
	if len(connections) == 0 {
		return "", errors.New("MySQL credential context requires a connection")
	}
	seen := make(map[string]bool, len(connections))
	for _, connection := range connections {
		if strings.TrimSpace(connection.Host) == "" || connection.Port <= 0 || connection.Port > 65535 ||
			strings.TrimSpace(connection.User) == "" || connection.Password == "" ||
			strings.ContainsAny(connection.User, "\x00\r\n") || strings.ContainsAny(connection.Password, "\x00\r\n") {
			return "", errors.New("invalid MySQL connection credential")
		}
		key := strings.ToLower(strings.TrimSpace(connection.Host)) + ":" + fmt.Sprint(connection.Port)
		if seen[key] {
			return "", errors.New("duplicate MySQL connection credential")
		}
		seen[key] = true
	}
	data, err := json.Marshal(mysqlJSONCredentialContext{Version: mysqlCredentialContextVersion, Connections: connections})
	if err != nil {
		return "", errors.New("unable to encode MySQL credential context")
	}
	return writeMySQLCredentialContextBytes(append(data, '\n'))
}

func writeMySQLCredentialContextBytes(contents []byte) (string, error) {
	file, err := createMySQLSecretContextFile()
	if err != nil {
		return "", errors.New("unable to create MySQL credential context")
	}
	name := file.Name()
	if err := file.Chmod(0o600); err != nil {
		return "", errors.Join(errors.New("unable to protect MySQL credential context"), cleanupMySQLSecretContext(file, name, false))
	}
	if _, err := file.Write(contents); err != nil {
		return "", errors.Join(errors.New("unable to write MySQL credential context"), cleanupMySQLSecretContext(file, name, false))
	}
	if err := file.Close(); err != nil {
		return "", errors.Join(errors.New("unable to close MySQL credential context"), cleanupMySQLSecretContext(file, name, true))
	}
	return name, nil
}

func removeMySQLCredentialContext(name string) error {
	if err := removeMySQLCredentialContextFile(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("unable to remove local MySQL credential context")
	}
	return nil
}

func sanitizeMySQLCredentialText(value string, secrets ...string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	return logmask.Mask(value)
}

type mysqlSanitizedLogger struct {
	base    Logger
	secrets []string
}

func (l mysqlSanitizedLogger) Info(format string, args ...any) {
	if l.base != nil {
		l.base.Info("%s", sanitizeMySQLCredentialText(fmt.Sprintf(format, args...), l.secrets...))
	}
}

func (l mysqlSanitizedLogger) Error(format string, args ...any) {
	if l.base != nil {
		l.base.Error("%s", sanitizeMySQLCredentialText(fmt.Sprintf(format, args...), l.secrets...))
	}
}

func mysqlCredentialSecrets(connections []mysqlConnectionCredential) []string {
	secrets := make([]string, 0, len(connections))
	for _, connection := range connections {
		if connection.Password != "" {
			secrets = append(secrets, connection.Password)
		}
	}
	return secrets
}

func mysqlCredentialCleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
}

func mysqlCredentialWorkCleanupCommand(work string) string {
	return "set -eu; case " + installerkit.ShellQuote(work) + " in /aifar/apps/mysql/_backup/*|*/_work/mysql-credential-*) rm -rf -- " + installerkit.ShellQuote(work) + ";; *) exit 1;; esac"
}

func mysqlRemoteCredentialValidationCommand(secretPath string) string {
	quoted := installerkit.ShellQuote(secretPath)
	return "set -eu; test -f " + quoted + "; test ! -L " + quoted + "; test \"$(stat -c '%u' " + quoted + ")\" = \"$(id -u)\"; test \"$(stat -c '%a' " + quoted + ")\" = 600"
}

func uploadMySQLCredentialContext(ctx context.Context, remote Remote, server store.Server, credential store.Credential, port int, remotePath string) error {
	localPath, err := writeMySQLSecretContext(credential, port)
	if err != nil {
		return errors.New("unable to create MySQL credential context")
	}
	uploadErr := remote.UploadFile(ctx, server, localPath, remotePath, 0o600)
	cleanupErr := removeMySQLCredentialContext(localPath)
	if uploadErr != nil || cleanupErr != nil {
		return errors.New("unable to transfer MySQL credential context")
	}
	return nil
}

func (s Service) withMySQLCredentialWork(ctx context.Context, server store.Server, work string, credential store.Credential, port int, run func() error) (retErr error) {
	if _, err := s.remote.Run(ctx, server, bootstrapBackupWorkCommand(work)); err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := mysqlCredentialCleanupContext(ctx)
		defer cancel()
		if _, err := s.remote.Run(cleanupCtx, server, cleanupBackupCommand(work)); err != nil {
			retErr = errors.Join(retErr, errors.New("unable to clean remote MySQL credential context"))
		}
	}()
	if err := uploadMySQLCredentialContext(ctx, s.remote, server, credential, port, path.Join(work, "secret-context.cnf")); err != nil {
		return err
	}
	return run()
}
