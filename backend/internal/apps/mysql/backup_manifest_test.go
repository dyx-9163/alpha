package mysql

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/store"
)

func validBackupManifest() BackupManifest {
	return BackupManifest{
		BackupID:          "backup_0123456789abcdef01234567",
		App:               "mysql",
		Topology:          "standalone",
		InstanceID:        "app_0123456789abcdef01234567",
		SourceServerID:    "srv_0123456789abcdef01234567",
		SourceEndpoint:    "127.0.0.1:3306",
		SourceServerUUID:  "11111111-1111-1111-1111-111111111111",
		MySQLVersion:      "8.0.36",
		MySQLShellVersion: "8.0.36",
		Schemas:           []string{"sales", "billing"},
		ExcludedSchemas:   []string{"mysql", "sys", "performance_schema", "information_schema", "mysql_innodb_cluster_metadata"},
		Consistent:        true,
		GTIDExecuted:      "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa:1-7",
		CreatedAt:         time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
		TaskID:            "tsk_0123456789abcdef01234567",
	}
}

func TestBackupManifestNormalizeRejectsUnsafeSchemasAndCanonicalizesBusinessSchemas(t *testing.T) {
	// Production break caught: accepting a system/SQL-injection-shaped schema would let a later dump or drop escape the manifest allowlist.
	manifest := validBackupManifest()
	manifest.Schemas = []string{"zebra", "billing"}

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

	for _, schema := range []string{"mysql", "MYSQL", "Information_Schema", "mysql_innodb_CLUSTER_metadata", "orders;DROP", "two words", "../outside"} {
		manifest := validBackupManifest()
		manifest.Schemas = []string{schema}
		if _, err := NormalizeBackupManifest(manifest); err == nil {
			t.Fatalf("schema %q unexpectedly accepted", schema)
		}
	}
}

func TestBackupManifestNormalizeRejectsCaseFoldedBusinessSchemaDuplicates(t *testing.T) {
	// Production break caught: treating Sales and sales as separate schemas would create an ambiguous dump/drop allowlist on case-insensitive MySQL deployments.
	manifest := validBackupManifest()
	manifest.Schemas = []string{"Sales", "sales"}
	if _, err := NormalizeBackupManifest(manifest); err == nil {
		t.Fatal("case-folded duplicate schemas unexpectedly accepted")
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

func TestContainsSecretShapeTraversesFutureJSONFields(t *testing.T) {
	// Production break caught: a future serialized field with a password/credential/privateKey name or bare secret-shaped value would bypass the manifest secret gate.
	type futureManifestField struct {
		PrivateKey string `json:"privateKey"`
	}
	for _, value := range []any{
		map[string]any{"password": "x"},
		[]any{map[string]any{"credential": "x"}},
		futureManifestField{PrivateKey: "x"},
		map[string]any{"safe": "secret"},
	} {
		if !containsSecretShape(value) {
			t.Fatalf("secret-shaped JSON value escaped traversal: %#v", value)
		}
	}
}

func TestBackupManifestRejectsNonCanonicalRequiredValues(t *testing.T) {
	// Production break caught: trimming or alias-normalizing persisted manifest fields would make two textual identities validate as the same backup source.
	cases := []struct {
		name   string
		mutate func(*BackupManifest)
	}{
		{"app whitespace", func(m *BackupManifest) { m.App = " mysql" }},
		{"topology case alias", func(m *BackupManifest) { m.Topology = "Standalone" }},
		{"backup id whitespace", func(m *BackupManifest) { m.BackupID = "backup-001 " }},
		{"instance id whitespace", func(m *BackupManifest) { m.InstanceID = " mysql-001" }},
		{"source server whitespace", func(m *BackupManifest) { m.SourceServerID = "server-001 " }},
		{"mysql version whitespace", func(m *BackupManifest) { m.MySQLVersion = "8.0.36 " }},
		{"mysql shell version whitespace", func(m *BackupManifest) { m.MySQLShellVersion = " 8.0.36" }},
		{"task id whitespace", func(m *BackupManifest) { m.TaskID = "task-001 " }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := validBackupManifest()
			tc.mutate(&manifest)
			if _, err := NormalizeBackupManifest(manifest); err == nil {
				t.Fatal("non-canonical required value unexpectedly accepted")
			}
		})
	}
	for _, backupType := range []string{" logical-full", "logical-full ", "LOGICAL-FULL"} {
		if err := ValidateRestoreCompatibility(validBackupManifest(), backupType, "standalone", "8.0.36"); err == nil {
			t.Fatalf("non-canonical backup type %q unexpectedly accepted", backupType)
		}
	}
}

func TestBackupManifestRejectsCaseAliasesForGeneratedIDsAndEndpointForms(t *testing.T) {
	// Production break caught: accepting an uppercase or alternate generated ID/endpoint representation would split one backup identity across manifest records.
	cases := []func(*BackupManifest){
		func(m *BackupManifest) { m.BackupID = "BACKUP-001" },
		func(m *BackupManifest) { m.InstanceID = "MYSQL-001" },
		func(m *BackupManifest) { m.SourceServerUUID = "AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA" },
		func(m *BackupManifest) { m.TaskID = "TASK-001" },
		func(m *BackupManifest) { m.SourceEndpoint = "127.0.0.1:03306" },
	}
	for _, mutate := range cases {
		manifest := validBackupManifest()
		mutate(&manifest)
		if _, err := NormalizeBackupManifest(manifest); err == nil {
			t.Fatal("noncanonical generated identity unexpectedly accepted")
		}
	}
	cluster := validBackupManifest()
	cluster.Topology, cluster.ClusterID = "innodb-cluster", "CLUSTER-001"
	cluster.SourceServerID, cluster.SourceEndpoint = "server-1", "10.0.0.1:3306"
	cluster.Members = []ClusterMemberRef{
		{InstanceID: "mysql-1", ServerID: "server-1", Endpoint: "10.0.0.1:3306", Role: "PRIMARY", Status: "ONLINE"},
		{InstanceID: "mysql-2", ServerID: "server-2", Endpoint: "10.0.0.2:3306", Role: "SECONDARY", Status: "ONLINE"},
		{InstanceID: "mysql-3", ServerID: "server-3", Endpoint: "10.0.0.3:3306", Role: "SECONDARY", Status: "ONLINE"},
	}
	if _, err := NormalizeBackupManifest(cluster); err == nil {
		t.Fatal("uppercase cluster ID unexpectedly accepted")
	}
}

func TestBackupManifestAcceptsCurrentStoreIDsAndCanonicalizesEndpointIdentity(t *testing.T) {
	// Production break caught: rejecting Store.NewID output or an existing uppercase DNS host would make current MySQL instances impossible to back up or restore.
	manifest := validBackupManifest()
	manifest.BackupID = store.NewID("backup")
	manifest.InstanceID = store.NewID("app")
	manifest.SourceServerID = store.NewID("srv")
	manifest.TaskID = store.NewID("tsk")
	manifest.SourceEndpoint = "DB.EXAMPLE.COM.:3306"
	normalized, err := NormalizeBackupManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := normalized.SourceEndpoint, "db.example.com:3306"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
	manifest.SourceEndpoint = "[2001:0db8::1]:3306"
	normalized, err = NormalizeBackupManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := normalized.SourceEndpoint, "[2001:db8::1]:3306"; got != want {
		t.Fatalf("IPv6 endpoint = %q, want %q", got, want)
	}
	for _, field := range []string{"BACKUP_0123456789abcdef01234567", "backup-0123456789abcdef01234567", " backup_0123456789abcdef01234567"} {
		broken := validBackupManifest()
		broken.BackupID = field
		if _, err := NormalizeBackupManifest(broken); err == nil {
			t.Fatalf("invalid backup ID %q accepted", field)
		}
	}
	for _, endpoint := range []string{"db example.com:3306", "db_.example.com:3306", "-db.example.com:3306", "db..example.com:3306"} {
		broken := validBackupManifest()
		broken.SourceEndpoint = endpoint
		if _, err := NormalizeBackupManifest(broken); err == nil {
			t.Fatalf("invalid endpoint %q accepted", endpoint)
		}
	}
}

func TestBackupManifestNormalizeRequiresHealthyDeterministicClusterMetadata(t *testing.T) {
	// Production break caught: accepting an incomplete or unhealthy cluster manifest would allow restore from an unknown source primary/topology.
	manifest := validBackupManifest()
	manifest.Topology = "innodb-cluster"
	manifest.ClusterID = "mysql_cluster_0123456789abcdef01234567"
	manifest.SourceServerID = "srv_111111111111111111111111"
	manifest.SourceEndpoint = "10.0.0.1:3306"
	manifest.Members = []ClusterMemberRef{
		{InstanceID: "app_333333333333333333333333", ServerID: "srv_333333333333333333333333", Endpoint: "10.0.0.3:3306", Role: "SECONDARY", Status: "ONLINE"},
		{InstanceID: "app_111111111111111111111111", ServerID: "srv_111111111111111111111111", Endpoint: "10.0.0.1:3306", Role: "PRIMARY", Status: "ONLINE"},
		{InstanceID: "app_222222222222222222222222", ServerID: "srv_222222222222222222222222", Endpoint: "10.0.0.2:3306", Role: "SECONDARY", Status: "ONLINE"},
	}

	normalized, err := NormalizeBackupManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := normalized.Members[0].InstanceID, "app_111111111111111111111111"; got != want {
		t.Fatalf("first member = %q, want %q", got, want)
	}
	if got, want := normalized.Members[0].Role, "PRIMARY"; got != want {
		t.Fatalf("primary role = %q, want %q", got, want)
	}

	for _, mutate := range []func(*BackupManifest){
		func(m *BackupManifest) { m.ClusterID = "" },
		func(m *BackupManifest) { m.Members[0].InstanceID = "app_111111111111111111111111" },
		func(m *BackupManifest) { m.Members[0].Status = "OFFLINE" },
		func(m *BackupManifest) { m.Members[1].Role = "SECONDARY" },
		func(m *BackupManifest) { m.Members = m.Members[:2] },
		func(m *BackupManifest) {
			m.Members = append(m.Members, ClusterMemberRef{InstanceID: "app_444444444444444444444444", ServerID: "srv_444444444444444444444444", Endpoint: "10.0.0.4:3306", Role: "SECONDARY", Status: "ONLINE"})
		},
		func(m *BackupManifest) { m.Members[0].Role = "primary " },
		func(m *BackupManifest) { m.Members[0].Status = "online" },
		func(m *BackupManifest) { m.Members[1].Role = "ARBITER" },
		func(m *BackupManifest) { m.Members[0].ServerID = "SRV_333333333333333333333333" },
		func(m *BackupManifest) { m.Members[0].Endpoint = "10.0.0.3:3306 " },
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
