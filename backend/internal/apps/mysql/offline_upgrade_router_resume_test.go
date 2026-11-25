package mysql

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

func TestOfflineUpgradeResumesInterruptedRouterSwitch(t *testing.T) {
	for _, tc := range []struct {
		name            string
		alreadyVerified bool
		wantReverted    []int
	}{
		{name: "first verification after interruption", wantReverted: []int{0}},
		{name: "previously verified Router", alreadyVerified: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, remote, resources, run := newOfflineUpgradeFixture(t)
			service := NewService(data, remote)
			seed := data.instances[0]
			ctx := context.Background()
			prior := offlineUpgrade{service: service, data: data, run: run, seed: seed, versions: map[int]string{}}
			if err := prior.preflight(ctx, resources); err != nil {
				t.Fatal(err)
			}
			if err := prior.stage(ctx); err != nil {
				t.Fatal(err)
			}
			if err := prior.backup(ctx); err != nil {
				t.Fatal(err)
			}
			for _, node := range prior.nodes {
				if _, err := prior.switchTools(ctx, node); err != nil {
					t.Fatal(err)
				}
			}
			router := prior.routers[0]
			if _, err := prior.switchTools(ctx, router); err != nil {
				t.Fatal(err)
			}
			if tc.alreadyVerified {
				if err := prior.verifyRouterAgainstLiveCluster(ctx, router); err != nil {
					t.Fatal(err)
				}
				if err := prior.recordVerified(router, true); err != nil {
					t.Fatal(err)
				}
			}

			run.TaskID = "tsk_new_manual_attempt"
			remote.failRoute = true
			if err := service.RunOfflineUpgrade(ctx, seed, resources, run); err == nil {
				t.Fatal("broken Router SQL route was accepted")
			}
			if !slices.Equal(remote.routerSwitches, []int{0}) || !slices.Equal(remote.routerReverts, tc.wantReverted) {
				t.Fatalf("unexpected Router operations: switched %v, reverted %v, want reverted %v", remote.routerSwitches, remote.routerReverts, tc.wantReverted)
			}
			if len(remote.rolled) != 0 || remote.metadata || data.checkpoint.TaskID != prior.run.TaskID {
				t.Fatal("the failed Router route must stop the rollout and keep the original checkpoint")
			}
		})
	}
}

func TestOfflineUpgradeResumeLockGuardOnlyChecksOriginalNodes(t *testing.T) {
	data, remote, resources, run := newOfflineUpgradeFixture(t)
	ctx := context.Background()
	seed := data.instances[0]
	prior := offlineUpgrade{service: NewService(data, remote), data: data, run: run, seed: seed, versions: map[int]string{}}
	if err := prior.preflight(ctx, resources); err != nil {
		t.Fatal(err)
	}
	if err := prior.stage(ctx); err != nil {
		t.Fatal(err)
	}
	if err := prior.backup(ctx); err != nil {
		t.Fatal(err)
	}

	check := &resumeIdleRemote{t: t}
	module := NewModule(data, check)
	if err := module.CheckOfflineUpgradeResumeIdle(ctx, seed, "different_task", run); err == nil || len(check.calls) != 0 {
		t.Fatalf("changed checkpoint must block SSH checks: %v, calls %v", err, check.calls)
	}
	check.busyAt = 2
	if err := module.CheckOfflineUpgradeResumeIdle(ctx, seed, run.TaskID, run); err == nil || len(check.calls) != 2 {
		t.Fatalf("busy old phase must block takeover: %v, calls %v", err, check.calls)
	}
	check.busyAt = 0
	check.calls = nil
	if err := module.CheckOfflineUpgradeResumeIdle(ctx, seed, run.TaskID, run); err != nil {
		t.Fatal(err)
	}
	if len(check.calls) != 4 || !strings.Contains(check.calls[3], "/mysql-router'") {
		t.Fatalf("idle guard did not verify the three MySQL roots and the original Router: %v", check.calls)
	}
}

type resumeIdleRemote struct {
	t      *testing.T
	calls  []string
	busyAt int
}

func (r *resumeIdleRemote) Run(ctx context.Context, _ store.Server, command string) (adapter.CommandResult, error) {
	r.t.Helper()
	if _, bounded := ctx.Deadline(); !bounded || !strings.Contains(command, "flock -n --") ||
		!strings.HasSuffix(command, " true") || strings.Contains(command, "systemctl") {
		r.t.Fatalf("resume guard should only verify the root and try its lock within a deadline: %s", command)
	}
	r.calls = append(r.calls, command)
	if r.busyAt == len(r.calls) {
		return adapter.CommandResult{ExitCode: 1}, nil
	}
	return adapter.CommandResult{}, nil
}

func (r *resumeIdleRemote) UploadFile(_ context.Context, _ store.Server, _, _ string, _ os.FileMode) error {
	r.t.Helper()
	r.t.Fatal("resume guard must not upload files")
	return nil
}
