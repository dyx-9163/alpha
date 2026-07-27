package mysql

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validBackupManifest() BackupManifest {
	return BackupManifest{
		BackupID:          "backup-001",
		App:               "mysql",
		Topology:          "standalone",
		InstanceID:        "mysql-001",
		SourceServerID:    "server-001",
		SourceEndpoint:    "127.0.0.1:3306",
		SourceServerUUID:  "11111111-1111-1111-1111-111111111111",
		MySQLVersion:      "8.0.36",
		MySQLShellVersion: "8.0.36",
		Schemas:           []string{"sales", "billing"},
		ExcludedSchemas:   []string{"mysql", "sys", "performance_schema", "information_schema", "mysql_innodb_cluster_metadata"},
		Consistent:        true,
		GTIDExecuted:      "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa:1-7",
		CreatedAt:         time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
		TaskID:            "task-001",
	}
}

func TestBackupManifestNormalizeRejectsUnsafeSchemasAndCanonicalizesBusinessSchemas(t *testing.T) {
	// Production break caught: accepting a system/SQL-injection-shaped schema would let a later dump or drop escape the manifest allowlist.
	manifest := validBackupManifest()
	manifest.Schemas = []string{"zebra", "billing", "zebra"}

	normalized, err := NormalizeBackupManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(normalized.Schemas, ","), "billing,zebra"; got != want {
		t.Fatalf("schemas = %q, want %q", got, want)
	}
	if got, want := strings.Join(normalized.ExcludedSchemas, ","), "information_schema,mysql,mysql_innodb_cluster_metadata,performance_schema,sys"; got != want {
		t.Fatalf("excluded schemas = %q, want %q", got, want)
	}

	for _, schema := range []string{"mysql", "orders;DROP", "two words", "../outside"} {
		manifest := validBackupManifest()
		manifest.Schemas = []string{schema}
		if _, err := NormalizeBackupManifest(manifest); err == nil {
			t.Fatalf("schema %q unexpectedly accepted", schema)
		}
	}
}

func TestBackupManifestNormalizeRejectsInvalidRequiredFieldsAndSecretShapedValues(t *testing.T) {
	// Production break caught: omitting a provenance/version field or serializing secret-shaped data would make a backup unsafe or unverifiable.
	cases := []struct {
		name   string
		mutate func(*BackupManifest)
	}{
		{"app", func(m *BackupManifest) { m.App = "postgres" }},
		{"topology", func(m *BackupManifest) { m.Topology = "router" }},
		{"install alias topology", func(m *BackupManifest) { m.Topology = "single" }},
		{"mysql version", func(m *BackupManifest) { m.MySQLVersion = "" }},
		{"mysql shell version", func(m *BackupManifest) { m.MySQLShellVersion = "" }},
		{"system schema exclusion", func(m *BackupManifest) { m.ExcludedSchemas = []string{"mysql"} }},
		{"consistent", func(m *BackupManifest) { m.Consistent = false }},
		{"secret shaped endpoint", func(m *BackupManifest) { m.SourceEndpoint = "mysql://root:password@127.0.0.1:3306" }},
		{"secret shaped gtid", func(m *BackupManifest) { m.GTIDExecuted = "password=do-not-store" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := validBackupManifest()
			tc.mutate(&manifest)
			if _, err := NormalizeBackupManifest(manifest); err == nil {
				t.Fatal("invalid manifest unexpectedly accepted")
			}
		})
	}
}

func TestBackupManifestCanonicalJSONContainsNoSecretShapedKeysOrValues(t *testing.T) {
	// Production break caught: adding a password-like field or value to the persisted manifest would leak credentials through repository, task, or audit reads.
	data, err := CanonicalBackupManifestJSON(validBackupManifest())
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if containsSecretShape(decoded) {
		t.Fatalf("canonical manifest contains secret-shaped JSON: %s", data)
	}
}

func TestBackupManifestNormalizeRequiresHealthyDeterministicClusterMetadata(t *testing.T) {
	// Production break caught: accepting an incomplete or unhealthy cluster manifest would allow restore from an unknown source primary/topology.
	manifest := validBackupManifest()
	manifest.Topology = "innodb-cluster"
	manifest.ClusterID = "cluster-001"
	manifest.SourceServerID = "server-1"
	manifest.SourceEndpoint = "10.0.0.1:3306"
	manifest.Members = []ClusterMemberRef{
		{InstanceID: "mysql-3", ServerID: "server-3", Endpoint: "10.0.0.3:3306", Role: "SECONDARY", Status: "ONLINE"},
		{InstanceID: "mysql-1", ServerID: "server-1", Endpoint: "10.0.0.1:3306", Role: "primary", Status: "online"},
		{InstanceID: "mysql-2", ServerID: "server-2", Endpoint: "10.0.0.2:3306", Role: "SECONDARY", Status: "ONLINE"},
	}

	normalized, err := NormalizeBackupManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := normalized.Members[0].InstanceID, "mysql-1"; got != want {
		t.Fatalf("first member = %q, want %q", got, want)
	}
	if got, want := normalized.Members[0].Role, "PRIMARY"; got != want {
		t.Fatalf("primary role = %q, want %q", got, want)
	}

	for _, mutate := range []func(*BackupManifest){
		func(m *BackupManifest) { m.ClusterID = "" },
		func(m *BackupManifest) { m.Members[0].InstanceID = "mysql-1" },
		func(m *BackupManifest) { m.Members[0].Status = "OFFLINE" },
		func(m *BackupManifest) { m.Members[1].Role = "SECONDARY" },
		func(m *BackupManifest) { m.Members = m.Members[:2] },
	} {
		broken := manifest
		broken.Members = append([]ClusterMemberRef(nil), manifest.Members...)
		mutate(&broken)
		if _, err := NormalizeBackupManifest(broken); err == nil {
			t.Fatal("incomplete cluster manifest unexpectedly accepted")
		}
	}
}

func TestBackupManifestRestoreCompatibilityRejectsTopologyVersionAndBackupTypeMismatches(t *testing.T) {
	// Production break caught: allowing cross-topology, cross-full-version, or unsupported backup types could overwrite an incompatible MySQL target.
	manifest := validBackupManifest()
	if err := ValidateRestoreCompatibility(manifest, "logical-full", "standalone", "8.0.36"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		backupType string
		topology   string
		version    string
	}{
		{"incremental", "standalone", "8.0.36"},
		{"logical-full", "innodb-cluster", "8.0.36"},
		{"pre-restore", "standalone", "8.0.37"},
	} {
		if err := ValidateRestoreCompatibility(manifest, tc.backupType, tc.topology, tc.version); err == nil {
			t.Fatalf("incompatible restore unexpectedly accepted: %+v", tc)
		}
	}
}

func TestMySQLBackupErrorTextResolvesEveryStableCodeWithoutCredentialDetails(t *testing.T) {
	// Production break caught: losing a zh/en message for a stable backup/restore code would expose an opaque code or leak credential diagnostics to an operator.
	for code := range mysqlBackupErrorMessageKeys {
		for _, language := range []string{"zh", "en"} {
			message := MySQLBackupErrorText(language, code)
			if message == "" || message == code {
				t.Fatalf("%s has no %s message", code, language)
			}
			if code == MySQLCredentialUnavailable && (strings.Contains(strings.ToLower(message), "secret") || strings.Contains(strings.ToLower(message), "record")) {
				t.Fatalf("credential-unavailable message leaks implementation detail: %q", message)
			}
		}
	}
}
