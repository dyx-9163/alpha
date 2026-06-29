package rbac

import "testing"

func TestAllowsOwnerAllPermissions(t *testing.T) {
	for _, permission := range []Permission{SettingsManage, AuditManage, AppsManage, TerminalConnect} {
		if !Allows("owner", permission) {
			t.Fatalf("expected owner to allow %s", permission)
		}
	}
}

func TestAllowsOperatorOperationalPermissionsOnly(t *testing.T) {
	if !Allows("operator", AppsManage) || !Allows("operator", TerminalConnect) {
		t.Fatalf("expected operator to manage apps and terminals")
	}
	if Allows("operator", SettingsManage) || Allows("operator", AuditManage) {
		t.Fatalf("operator must not manage settings or audit retention")
	}
}

func TestAllowsViewerNoMutationPermissions(t *testing.T) {
	if Allows("viewer", ServersManage) || Allows("viewer", StorageManage) {
		t.Fatalf("viewer must not allow mutation permissions")
	}
	if Allows("", AppsManage) || Allows("unknown", AppsManage) {
		t.Fatalf("empty or unknown roles must not allow protected permissions")
	}
}
