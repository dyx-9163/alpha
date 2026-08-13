package collector

import (
	"context"
	"errors"
	"sync"
	"time"

	"aifar-deployment/backend/internal/logmask"
	"aifar-deployment/backend/internal/store"
)

func (m *Manager) collectLiveServers(ctx context.Context) error {
	servers, err := m.store.ListServers()
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		return nil
	}

	workers := m.serverProbeWorkers
	if workers <= 0 {
		workers = 1
	}
	if workers > len(servers) {
		workers = len(servers)
	}
	jobs := make(chan store.Server)
	results := make(chan error, len(servers))
	var wg sync.WaitGroup
	for index := 0; index < workers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for server := range jobs {
				results <- m.collectOneServer(ctx, server)
			}
		}()
	}

	scheduled := 0
schedule:
	for _, server := range servers {
		if !m.tryStart("server:" + server.ID) {
			continue
		}
		select {
		case jobs <- server:
			scheduled++
		case <-ctx.Done():
			m.finish("server:" + server.ID)
			break schedule
		}
	}
	close(jobs)
	wg.Wait()
	close(results)
	if scheduled == 0 {
		return errCollectorFamilyInFlight
	}

	errs := make([]error, 0)
	for index := 0; index < scheduled; index++ {
		if result := <-results; result != nil {
			errs = append(errs, result)
		}
	}
	if ctx.Err() != nil {
		errs = append(errs, ctx.Err())
	}
	return errors.Join(errs...)
}

func (m *Manager) collectOneServer(ctx context.Context, public store.Server) error {
	defer m.finish("server:" + public.ID)
	status := "available"
	errText := ""
	server, err := m.store.GetServer(public.ID, true)
	if err != nil && !errors.Is(err, store.ErrServerCredentialDecryption) {
		return err
	}
	if err == nil {
		timeout := m.serverProbeTimeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		child, cancel := context.WithTimeout(ctx, timeout)
		err = m.serverProbe(child, server)
		cancel()
	}
	if err != nil {
		status = "failed"
		errText = logmask.Mask(err.Error())
	}
	payload := map[string]any{
		"id":         public.ID,
		"name":       public.Name,
		"host":       public.Host,
		"status":     status,
		"dockerHost": public.DockerHost,
		"updatedAt":  public.UpdatedAt,
	}
	return m.saveSnapshotWithPolicy(ctx, store.StatusSnapshot{
		Scope:       "server",
		ResourceID:  public.ID,
		ServerID:    public.ID,
		Status:      status,
		LastError:   errText,
		Payload:     marshalPayload(payload),
		CollectedAt: time.Now(),
	}, false)
}
