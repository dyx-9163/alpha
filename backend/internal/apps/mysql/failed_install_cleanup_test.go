package mysql

import (
	"testing"

	"aifar-deployment/backend/internal/store"
)

func TestIsFailedInstallCleanupInstanceRequiresControlledSyntheticGroup(t *testing.T) {
	const valid = `{"installFailed":true,"taskId":"tsk_1234567890abcdef12345678","clusterId":"mysql-failed-tsk_1234567890abcdef12345678","topology":"innodb-cluster"}`
	tests := []struct {
		name     string
		status   string
		metadata string
		want     bool
	}{
		{name: "controlled failed placeholder", status: "failed", metadata: valid, want: true},
		{name: "installed cluster", status: "installed", metadata: valid, want: false},
		{name: "forged cluster identity", status: "failed", metadata: `{"installFailed":true,"taskId":"tsk_1234567890abcdef12345678","clusterId":"cluster_1234567890abcdef12345678","topology":"innodb-cluster"}`, want: false},
		{name: "uncontrolled task identity", status: "failed", metadata: `{"installFailed":true,"taskId":"task-failed","clusterId":"mysql-failed-task-failed","topology":"innodb-cluster"}`, want: false},
		{name: "maintenance state present", status: "failed", metadata: `{"installFailed":true,"taskId":"tsk_1234567890abcdef12345678","clusterId":"mysql-failed-tsk_1234567890abcdef12345678","topology":"innodb-cluster","mysqlMaintenance":{}}`, want: false},
		{name: "reconciliation state present", status: "failed", metadata: `{"installFailed":true,"taskId":"tsk_1234567890abcdef12345678","clusterId":"mysql-failed-tsk_1234567890abcdef12345678","topology":"innodb-cluster","mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-29T00:00:00Z","taskId":"tsk_abcdefabcdefabcdefabcdef"}}`, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance := store.AppInstance{App: "mysql", Status: test.status, Topology: "innodb-cluster", Metadata: test.metadata}
			if got := IsFailedInstallCleanupInstance(instance); got != test.want {
				t.Fatalf("cleanup classification=%v want=%v metadata=%s", got, test.want, test.metadata)
			}
		})
	}
}
