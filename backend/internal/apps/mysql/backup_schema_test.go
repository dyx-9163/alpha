package mysql

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

func TestDiscoverBackupSchemasLoadsSSHSecretForLiveQuery(t *testing.T) {
	module, data, _ := newStandaloneBackupModule(t)
	request := standaloneBackupRequest(t)
	catalog, err := module.DiscoverBackupSchemas(context.Background(), registry.BackupRequest{
		Instance: request.Instance, Servers: request.Servers, Language: request.Language,
	})
	if err != nil {
		t.Fatal(err)
	}
	if catalog.InstanceID != request.Instance.ID || !reflect.DeepEqual(data.serverSecretRequests, []bool{true}) {
		t.Fatalf("catalog/server secret requests = %+v/%v, want instance %s and [true]", catalog, data.serverSecretRequests, request.Instance.ID)
	}
}

// Production break caught: loading persisted cluster members without their SSH
// secret makes PRIMARY discovery fail before the live schema query can run.
func TestDiscoverBackupSchemasClusterLoadsSSHSecretForPrimaryDiscovery(t *testing.T) {
	instances, servers := healthyClusterRequestFixtures()
	data := &schemaDiscoverySecretStore{clusterBackupFixture: newClusterBackupFixture(t, instances, servers)}
	remote := &schemaDiscoverySecretRemote{clusterBackupRemote: &clusterBackupRemote{
		backupFakeRemote: newBackupFakeRemote(),
		runtime:          healthyClusterRuntime(servers),
	}}

	catalog, err := NewModule(data, remote).DiscoverBackupSchemas(context.Background(), registry.BackupRequest{
		Instance: instances[1], Instances: instances, Servers: servers, Language: "en",
	})
	if err != nil {
		t.Fatal(err)
	}
	if catalog.InstanceID != instances[1].ID || catalog.SourceInstanceID != instances[0].ID || catalog.SourceServerID != servers[0].ID || len(catalog.Schemas) == 0 {
		t.Fatalf("cluster schema catalog = %+v", catalog)
	}
}

type schemaDiscoverySecretStore struct {
	*clusterBackupFixture
}

func (s *schemaDiscoverySecretStore) GetServer(id string, includeSecret bool) (store.Server, error) {
	server, err := s.clusterBackupFixture.GetServer(id, includeSecret)
	if err != nil {
		return store.Server{}, err
	}
	server.Password = ""
	if includeSecret {
		server.Password = "ssh-secret"
	}
	return server, nil
}

type schemaDiscoverySecretRemote struct {
	*clusterBackupRemote
}

func (r *schemaDiscoverySecretRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	if server.Password == "" {
		return adapter.CommandResult{}, errors.New("server has no usable SSH credential")
	}
	return r.clusterBackupRemote.Run(ctx, server, command)
}

func TestClassifyBackupSchemasSeparatesServerMetadataAndBusiness(t *testing.T) {
	available := []mysqlBackupSchema{
		{Name: "information_schema"}, {Name: "mysql"},
		{Name: "mysql_innodb_cluster_metadata"}, {Name: "mysql_innodb_cluster_metadata_bkp"},
		{Name: "mysql_innodb_cluster_metadata_previous"}, {Name: "performance_schema"}, {Name: "sys"},
		{Name: "orders", EstimatedBytes: 128}, {Name: "billing", EstimatedBytes: 64},
	}
	classified, err := classifyBackupSchemas(available)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"information_schema": "server-system", "mysql": "server-system", "performance_schema": "server-system", "sys": "server-system",
		"mysql_innodb_cluster_metadata": "cluster-metadata", "mysql_innodb_cluster_metadata_bkp": "cluster-metadata",
		"mysql_innodb_cluster_metadata_previous": "cluster-metadata", "orders": "business", "billing": "business",
	}
	for _, schema := range classified {
		if got := string(schema.Category); got != want[schema.Name] {
			t.Fatalf("schema %s category=%q want=%q", schema.Name, got, want[schema.Name])
		}
		selectable := want[schema.Name] == "business"
		if schema.Selectable != selectable || schema.SelectedByDefault != selectable {
			t.Fatalf("schema %s selectable/default=%v/%v want=%v", schema.Name, schema.Selectable, schema.SelectedByDefault, selectable)
		}
	}
}

func TestSelectBackupSchemasCanonicalizesLiveNamesAndBuildsExclusions(t *testing.T) {
	available := []mysqlBackupSchema{{Name: "mysql"}, {Name: "mysql_innodb_cluster_metadata_bkp"}, {Name: "Billing", EstimatedBytes: 64}, {Name: "orders", EstimatedBytes: 128}}
	selected, excluded, estimated, err := selectBackupSchemas(available, []string{"ORDERS"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(selected, []string{"orders"}) || estimated != 128 {
		t.Fatalf("selected=%v estimated=%d", selected, estimated)
	}
	for _, required := range []string{"information_schema", "mysql", "mysql_innodb_cluster_metadata", "mysql_innodb_cluster_metadata_bkp", "performance_schema", "sys", "Billing"} {
		if !stringSliceContains(excluded, required) {
			t.Fatalf("excluded schemas %v missing %q", excluded, required)
		}
	}
}

func TestSelectBackupSchemasRejectsUnsafeOrStaleSelection(t *testing.T) {
	available := []mysqlBackupSchema{{Name: "mysql"}, {Name: "mysql_innodb_cluster_metadata"}, {Name: "orders", EstimatedBytes: 1}}
	for _, test := range []struct {
		name      string
		requested []string
	}{
		{name: "empty"}, {name: "duplicate", requested: []string{"orders", "ORDERS"}},
		{name: "server system", requested: []string{"mysql"}}, {name: "cluster metadata", requested: []string{"mysql_innodb_cluster_metadata"}},
		{name: "unknown", requested: []string{"missing"}}, {name: "invalid", requested: []string{"orders;DROP DATABASE mysql"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, err := selectBackupSchemas(available, test.requested); err == nil {
				t.Fatal("unsafe selection accepted")
			}
		})
	}
}

func TestSelectedSchemaParameterDistinguishesOmittedFromInvalid(t *testing.T) {
	if selected, present := selectedSchemaParameter(map[string]any{"name": "pre-restore"}); present || selected != nil {
		t.Fatalf("omitted schemas = %v/%v, want nil/false", selected, present)
	}
	for _, parameters := range []map[string]any{
		{"schemas": []string{}},
		{"schemas": "orders"},
		{"schemas": []any{"orders", 42}},
	} {
		selected, present := selectedSchemaParameter(parameters)
		if !present || len(selected) != 0 {
			t.Fatalf("invalid explicit schemas = %v/%v, want empty/true", selected, present)
		}
	}
}

func stringSliceContains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
