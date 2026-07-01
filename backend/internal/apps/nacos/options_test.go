package nacos

import "testing"

func TestClusterServerIDsPreferExplicitNacosServers(t *testing.T) {
	got := clusterServerIDs(map[string]any{
		"nacosServerIds": []any{"a", "b", "a", "c"},
	}, []string{"fallback"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestNacosOptionsValidateClusterDatabase(t *testing.T) {
	options := nacosOptions(map[string]any{
		"dbHost":     "192.168.1.10",
		"dbPort":     3306,
		"dbName":     "nacos_config",
		"dbUser":     "nacos",
		"dbPassword": "Oversea.123",
	}, "cluster")
	if err := options.Validate(); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}
