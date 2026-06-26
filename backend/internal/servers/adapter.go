package servers

import (
	"context"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

type SSHProber struct{}

func (SSHProber) Probe(ctx context.Context, server store.Server) error {
	return adapter.ProbeSSH(ctx, server)
}
