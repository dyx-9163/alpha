package taskmeta

import "testing"

func TestDescribeCategorizesAndMarksTrackableTasks(t *testing.T) {
	tests := []struct {
		taskType  string
		category  string
		trackable bool
	}{
		{"apps.mysql.install", "apps", true},
		{"apps.aifar.update-artifact", "apps", true},
		{"apps.delete.batch", "apps", true},
		{"apps.nacos.config.rollback", "apps", true},
		{"aifar.scale.out", "apps", true},
		{"database.mysql.cluster.start", "database", true},
		{"containers.container.restart", "containers", true},
		{"servers.probe", "servers", true},
		{"servers.telemetry", "servers", false},
		{"maintenance.database.backup", "maintenance", true},
		{"audit.delete", "audit", false},
		{"unknown.action", "other", false},
	}
	for _, tt := range tests {
		t.Run(tt.taskType, func(t *testing.T) {
			got := Describe(tt.taskType)
			if got.Category != tt.category || got.Trackable != tt.trackable {
				t.Fatalf("Describe(%q) = %+v, want category=%s trackable=%v", tt.taskType, got, tt.category, tt.trackable)
			}
		})
	}
}
