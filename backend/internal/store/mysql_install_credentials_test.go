package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestMySQLInstallCredentialTransactionsRejectClusterIdentityDrift(t *testing.T) {
	for _, operation := range []string{"bind", "mark-failed"} {
		t.Run(operation, func(t *testing.T) {
			s, err := OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "test-secret")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()

			instances := make([]AppInstance, 0, 4)
			for index := 0; index < 4; index++ {
				instance, err := s.SaveAppInstance(AppInstance{
					App:      "mysql",
					Version:  "8.0.36",
					ServerID: fmt.Sprintf("srv-%d", index+1),
					Status:   "installed",
					Topology: "innodb-cluster",
					Metadata: `{"clusterId":"cluster-original"}`,
				})
				if err != nil {
					t.Fatal(err)
				}
				instances = append(instances, instance)
			}

			drifted := instances[2]
			drifted.Metadata = `{"clusterId":"cluster-replaced"}`
			if _, err := s.SaveAppInstance(drifted); err != nil {
				t.Fatal(err)
			}

			switch operation {
			case "bind":
				items := make([]MySQLInstallAdminCredential, 0, len(instances))
				for _, instance := range instances {
					items = append(items, MySQLInstallAdminCredential{
						Instance: instance,
						Credential: Credential{
							Name: "generated-admin", Kind: "mysql", Username: "root",
							Secret: map[string]string{"password": "secret"},
						},
						Generated: true,
					})
				}
				if err := s.SaveMySQLInstallAdminCredentials(items); !errors.Is(err, ErrMySQLInstallAdminCredentialBinding) {
					t.Fatalf("expected cluster drift rejection, got %v", err)
				}
				credentials, err := s.ListCredentials(CredentialQuery{Kind: "mysql"})
				if err != nil {
					t.Fatal(err)
				}
				if len(credentials) != 0 {
					t.Fatalf("failed transaction retained %d credential(s)", len(credentials))
				}
			case "mark-failed":
				failed := append([]AppInstance(nil), instances...)
				for index := range failed {
					failed[index].Status = "failed"
					failed[index].Metadata = `{"clusterId":"cluster-original","installFailed":true}`
				}
				if err := s.MarkMySQLInstallInstancesFailed(failed); !errors.Is(err, ErrMySQLInstallAdminCredentialBinding) {
					t.Fatalf("expected cluster drift rejection, got %v", err)
				}
				for _, instance := range instances {
					current, err := s.GetAppInstance(instance.ID)
					if err != nil {
						t.Fatal(err)
					}
					if current.Status != "installed" {
						t.Fatalf("instance %s changed despite transaction rejection", instance.ID)
					}
				}
			}
		})
	}
}
