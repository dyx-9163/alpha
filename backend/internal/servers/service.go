package servers

import (
	"context"
	"errors"
	"strings"

	"aifar-deployment/backend/internal/store"
)

type Store interface {
	ListServers() ([]store.Server, error)
	GetServer(id string, includeSecret bool) (store.Server, error)
	SaveServer(v store.Server) (store.Server, error)
	ReorderServers(ids []string) error
	DeleteServer(id string) error
}

type Prober interface {
	Probe(ctx context.Context, server store.Server) error
}

type Logger interface {
	Info(format string, args ...any)
	Error(format string, args ...any)
	PlanTarget(target string)
	StartTarget(target string)
	FinishTarget(target, status, errText string)
	PlanStep(target, name, title string, order int)
	StartStep(target, name, title string, order int)
	FinishStep(target, name, status, errText string)
}

type Service struct {
	store            Store
	prober           Prober
	defaultDeployDir string
}

type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string {
	return e.Message
}

func IsValidationError(err error) bool {
	var target ValidationError
	return errors.As(err, &target)
}

func NewService(s Store, prober Prober, defaultDeployDir ...string) Service {
	deployDir := "/aifar/apps"
	if len(defaultDeployDir) > 0 && strings.TrimSpace(defaultDeployDir[0]) != "" {
		deployDir = strings.TrimSpace(defaultDeployDir[0])
	}
	return Service{store: s, prober: prober, defaultDeployDir: deployDir}
}

func (s Service) List() ([]store.Server, error) {
	return s.store.ListServers()
}

func (s Service) Save(input store.Server, lang string) (store.Server, error) {
	server := normalize(input, s.defaultDeployDir)
	if err := validate(server, lang); err != nil {
		return store.Server{}, err
	}
	return s.store.SaveServer(server)
}

func (s Service) Delete(id string) error {
	return s.store.DeleteServer(id)
}

func (s Service) Reorder(ids []string) error {
	return s.store.ReorderServers(ids)
}

func (s Service) Probe(ctx context.Context, id string, lang string, log Logger) error {
	copy := CopyFor(lang)
	log.PlanTarget(id)
	var server store.Server
	steps := []struct {
		name  string
		title string
		run   func(store.Server) error
	}{
		{name: "load-server", title: copy.LoadServer, run: func(_ store.Server) error {
			var err error
			server, err = s.store.GetServer(id, true)
			return err
		}},
		{name: "check-credential", title: copy.CheckCredential, run: func(server store.Server) error {
			if strings.TrimSpace(server.Password) == "" && strings.TrimSpace(server.PrivateKey) == "" {
				return errors.New(copy.CredentialMissing)
			}
			return nil
		}},
		{name: "probe-ssh", title: copy.ProbeSSH, run: func(server store.Server) error {
			log.Info(copy.ProbingServer, server.Username, server.Host, server.Port)
			return s.prober.Probe(ctx, server)
		}},
		{name: "collect-runtime", title: copy.CollectRuntime, run: func(server store.Server) error {
			log.Info("%s", copy.RuntimePlaceholder)
			return nil
		}},
	}
	for idx, step := range steps {
		log.PlanStep(id, step.name, step.title, idx+1)
	}
	log.StartTarget(id)
	for idx, step := range steps {
		if ctx.Err() != nil {
			log.FinishTarget(id, "cancelled", ctx.Err().Error())
			return ctx.Err()
		}
		log.StartStep(id, step.name, step.title, idx+1)
		if err := step.run(server); err != nil {
			log.FinishStep(id, step.name, "failed", err.Error())
			log.FinishTarget(id, "failed", err.Error())
			s.markProbeFailed(server, err)
			return err
		}
		log.FinishStep(id, step.name, "success", "")
	}
	server.Status = "available"
	server.LastError = ""
	if _, err := s.store.SaveServer(server); err != nil {
		log.FinishTarget(id, "failed", err.Error())
		return err
	}
	log.Info("%s", copy.ProbeSucceeded)
	log.FinishTarget(id, "success", "")
	return nil
}

func (s Service) markProbeFailed(server store.Server, err error) {
	if server.ID == "" || err == nil {
		return
	}
	server.Status = "failed"
	server.LastError = err.Error()
	_, _ = s.store.SaveServer(server)
}

func normalize(server store.Server, defaultDeployDir string) store.Server {
	server.Name = strings.TrimSpace(server.Name)
	server.Host = strings.TrimSpace(server.Host)
	server.Username = strings.TrimSpace(server.Username)
	server.AuthType = strings.TrimSpace(server.AuthType)
	server.DeployDir = strings.TrimSpace(server.DeployDir)
	server.DockerHost = strings.TrimSpace(server.DockerHost)
	defaultDeployDir = strings.TrimSpace(defaultDeployDir)
	if defaultDeployDir == "" {
		defaultDeployDir = "/aifar/apps"
	}
	if server.Port == 0 {
		server.Port = 22
	}
	if server.AuthType == "" {
		server.AuthType = "password"
	}
	if server.DeployDir == "" {
		server.DeployDir = defaultDeployDir
	}
	if server.Status == "" {
		server.Status = "unknown"
	}
	return server
}

func validate(server store.Server, lang string) error {
	if server.Name == "" || server.Host == "" || server.Username == "" {
		return ValidationError{Message: CopyFor(lang).ValidateServer}
	}
	return nil
}
