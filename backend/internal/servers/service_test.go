package servers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

type fakeStore struct {
	server store.Server
}

func (f *fakeStore) ListServers() ([]store.Server, error) {
	return []store.Server{f.server}, nil
}

func (f *fakeStore) GetServer(id string, includeSecret bool) (store.Server, error) {
	return f.server, nil
}

func (f *fakeStore) SaveServer(v store.Server) (store.Server, error) {
	f.server = v
	return v, nil
}

func (f *fakeStore) ReorderServers(ids []string) error {
	return nil
}

func (f *fakeStore) DeleteServer(id string) error {
	f.server = store.Server{}
	return nil
}

type fakeProber struct {
	err error
}

func (f fakeProber) Probe(ctx context.Context, server store.Server) error {
	return f.err
}

type fakeRemote struct {
	stdout  string
	command string
	err     error
}

func (f *fakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	f.command = command
	if f.err != nil {
		return adapter.CommandResult{}, f.err
	}
	return adapter.CommandResult{Stdout: f.stdout}, nil
}

type fakeLogger struct {
	steps   []string
	targets []string
}

func (f *fakeLogger) Info(format string, args ...any)  {}
func (f *fakeLogger) Error(format string, args ...any) {}
func (f *fakeLogger) PlanTarget(target string)         { f.targets = append(f.targets, "plan:"+target) }
func (f *fakeLogger) StartTarget(target string)        { f.targets = append(f.targets, "start:"+target) }
func (f *fakeLogger) FinishTarget(target, status, errText string) {
	f.targets = append(f.targets, status+":"+target)
}
func (f *fakeLogger) PlanStep(target, name, title string, order int) {
	f.steps = append(f.steps, "plan:"+name)
}
func (f *fakeLogger) StartStep(target, name, title string, order int) {
	f.steps = append(f.steps, "start:"+name)
}
func (f *fakeLogger) FinishStep(target, name, status, errText string) {
	f.steps = append(f.steps, status+":"+name)
}

func TestSaveValidatesServerIdentity(t *testing.T) {
	service := NewService(&fakeStore{}, fakeProber{})
	_, err := service.Save(store.Server{Host: "10.0.0.1", Username: "root"}, "en")
	if !IsValidationError(err) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestSaveUsesConfiguredDefaultDeployDir(t *testing.T) {
	s := &fakeStore{}
	service := NewService(s, fakeProber{}, "/aifar/apps")
	saved, err := service.Save(store.Server{Name: "db-1", Host: "10.0.0.1", Username: "root"}, "en")
	if err != nil {
		t.Fatal(err)
	}
	if saved.DeployDir != "/aifar/apps" {
		t.Fatalf("expected configured deploy dir, got %s", saved.DeployDir)
	}
}

func TestProbeRecordsEnterpriseSteps(t *testing.T) {
	s := &fakeStore{server: store.Server{ID: "srv-1", Name: "db-1", Host: "10.0.0.1", Port: 22, Username: "root", Password: "secret"}}
	service := NewService(s, fakeProber{})
	log := &fakeLogger{}
	if err := service.Probe(context.Background(), "srv-1", "en", log); err != nil {
		t.Fatal(err)
	}
	if s.server.Status != "available" {
		t.Fatalf("expected available status, got %s", s.server.Status)
	}
	if len(log.steps) < 12 {
		t.Fatalf("expected planned, started, and finished steps, got %+v", log.steps)
	}
}

func TestProbeFailureUpdatesServerStatus(t *testing.T) {
	probeErr := errors.New("ssh refused")
	s := &fakeStore{server: store.Server{ID: "srv-1", Name: "db-1", Host: "10.0.0.1", Port: 22, Username: "root", Password: "secret"}}
	service := NewService(s, fakeProber{err: probeErr})
	if err := service.Probe(context.Background(), "srv-1", "en", &fakeLogger{}); err == nil {
		t.Fatal("expected probe error")
	}
	if s.server.Status != "failed" {
		t.Fatalf("expected failed status, got %s", s.server.Status)
	}
	if s.server.LastError != probeErr.Error() {
		t.Fatalf("expected last error %q, got %q", probeErr.Error(), s.server.LastError)
	}
}

func TestListDiskDevicesDetectsUnmountedCandidates(t *testing.T) {
	remote := &fakeRemote{stdout: `{
		"blockdevices": [
			{"name":"sda","path":"/dev/sda","type":"disk","size":107374182400,"mountpoint":null,"fstype":null,"model":"system","ro":false,"rm":false,"children":[
				{"name":"sda1","path":"/dev/sda1","type":"part","size":1073741824,"mountpoint":"/boot","fstype":"xfs","model":"","ro":false,"rm":false},
				{"name":"sda2","path":"/dev/sda2","type":"part","size":106300440576,"mountpoint":"/","fstype":"xfs","model":"","ro":false,"rm":false},
				{"name":"sda3","path":"/dev/sda3","type":"part","size":8589934592,"mountpoint":null,"fstype":null,"model":"","ro":false,"rm":false}
			]},
			{"name":"sdb","path":"/dev/sdb","type":"disk","size":214748364800,"mountpoint":null,"fstype":null,"model":"data","ro":false,"rm":false},
			{"name":"sdc","path":"/dev/sdc","type":"disk","size":8589934592,"mountpoint":null,"fstype":null,"model":"usb","ro":false,"rm":true},
			{"name":"sr0","path":"/dev/sr0","type":"rom","size":1073741312,"mountpoint":null,"fstype":"iso9660","model":"cdrom","ro":true,"rm":true}
		]
	}`}
	service := NewServiceWithRemote(
		&fakeStore{server: store.Server{ID: "srv-1", Name: "s3-1", Host: "10.0.0.1", Username: "root", Password: "secret"}},
		fakeProber{},
		remote,
	)

	inventory, err := service.ListDiskDevices(context.Background(), "srv-1")
	if err != nil {
		t.Fatal(err)
	}
	if inventory.ServerID != "srv-1" {
		t.Fatalf("expected server id srv-1, got %s", inventory.ServerID)
	}
	if !strings.Contains(remote.command, "lsblk -b -J") || !strings.Contains(remote.command, "-e 7,11") {
		t.Fatalf("expected filtered lsblk json command, got %q", remote.command)
	}
	if len(inventory.Devices) != 1 {
		t.Fatalf("expected only selectable unmounted disks, got %+v", inventory.Devices)
	}
	dataDisk := inventory.Devices[0]
	if dataDisk.Path != "/dev/sdb" || !dataDisk.Candidate || dataDisk.SizeHuman == "" {
		t.Fatalf("expected /dev/sdb to be selectable, devices=%+v", inventory.Devices)
	}
}
