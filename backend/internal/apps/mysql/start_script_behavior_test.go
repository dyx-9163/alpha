package mysql

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInnoDBClusterStartAllowsEquivalentGTIDSupersetMembers(t *testing.T) {
	result := runInnoDBClusterStartJavaScript(t, map[string]string{
		"10.0.0.1": "gtid-equal",
		"10.0.0.2": "gtid-equal",
		"10.0.0.3": "gtid-equal",
	})
	if result.err != nil {
		t.Fatalf("equivalent GTID members should be recoverable: %v\n%s", result.err, result.output)
	}
	if !strings.Contains(result.output, "DRY_RUN 10.0.0.1") || !strings.Contains(result.output, "MUTATION 10.0.0.1") {
		t.Fatalf("recovery should dry-run and mutate from the first ordered equivalent member:\n%s", result.output)
	}
}

func TestInnoDBClusterStartRejectsDivergentGTIDSetsBeforeMutation(t *testing.T) {
	result := runInnoDBClusterStartJavaScript(t, map[string]string{
		"10.0.0.1": "gtid-a",
		"10.0.0.2": "gtid-b",
		"10.0.0.3": "gtid-c",
	})
	if result.err == nil {
		t.Fatalf("divergent GTID members should be rejected:\n%s", result.output)
	}
	if strings.Contains(result.output, "MUTATION ") {
		t.Fatalf("divergent GTID members reached a state-changing AdminAPI call:\n%s", result.output)
	}
}

type startJavaScriptResult struct {
	output string
	err    error
}

func runInnoDBClusterStartJavaScript(t *testing.T, gtids map[string]string) startJavaScriptResult {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to execute the rendered MySQL Shell JavaScript")
	}
	nodes := []InnoDBClusterNode{
		{Host: "10.0.0.1", Port: 3306},
		{Host: "10.0.0.2", Port: 3306},
		{Host: "10.0.0.3", Port: 3306},
	}
	script, err := startInnoDBClusterScript(InnoDBClusterStartScriptRequest{
		ClusterName:           "aifarCluster",
		InstallRoot:           "/aifar/apps/mysql",
		CredentialContextPath: "/aifar/apps/_work/mysql-credential-start-test/credential-context.json",
		Nodes:                 nodes,
	})
	if err != nil {
		t.Fatal(err)
	}
	javaScript := extractInnoDBClusterStartJavaScript(t, script)
	fixture, err := json.Marshal(gtids)
	if err != nil {
		t.Fatal(err)
	}
	harness := `
const fixtureGTIDs = JSON.parse(process.env.AIFAR_TEST_GTIDS);
let currentHost = '';
globalThis.os = {
  loadTextFile: () => JSON.stringify({
    version: 1,
    connections: Object.keys(fixtureGTIDs).map((host) => ({host, port: 3306, user: 'root', password: 'test-only'}))
  })
};
globalThis.shell = {
  connect: (connection) => { currentHost = connection.host; }
};
globalThis.session = {
  runSql: (sql, args) => {
    let rows;
    if (sql.indexOf('@@GLOBAL.gtid_executed') >= 0) {
      rows = [[fixtureGTIDs[currentHost]]];
    } else if (sql.indexOf('GTID_SUBSET') >= 0) {
      rows = [[args[0] === args[1] ? 1 : 0]];
    } else if (sql.indexOf('information_schema.tables') >= 0) {
      rows = [];
    } else {
      throw new Error('unexpected SQL in test harness: ' + sql);
    }
    return { fetchOne: () => rows.length === 0 ? null : rows.shift() };
  }
};
globalThis.print = (message) => process.stdout.write(String(message) + '\n');
globalThis.dba = {
  rebootClusterFromCompleteOutage: (_clusterName, options) => {
    if (options && options.dryRun === true) {
      process.stdout.write('DRY_RUN ' + currentHost + '\n');
      return;
    }
    process.stdout.write('MUTATION ' + currentHost + '\n');
    return { rejoinInstance: () => {}, status: () => {} };
  }
};
`
	testFile := filepath.Join(t.TempDir(), "start-cluster.js")
	if err := os.WriteFile(testFile, []byte(harness+"\n"+javaScript), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, testFile)
	command.Env = append(os.Environ(), "AIFAR_TEST_GTIDS="+string(fixture))
	output, runErr := command.CombinedOutput()
	return startJavaScriptResult{output: string(output), err: runErr}
}

func extractInnoDBClusterStartJavaScript(t *testing.T, script string) string {
	t.Helper()
	normalized := strings.ReplaceAll(script, "\r\n", "\n")
	startMarker := "<<'JS'\n"
	start := strings.Index(normalized, startMarker)
	if start < 0 {
		t.Fatal("rendered start script is missing the JavaScript heredoc")
	}
	start += len(startMarker)
	end := strings.Index(normalized[start:], "\nJS\n)")
	if end < 0 {
		t.Fatal("rendered start script is missing the JavaScript heredoc terminator")
	}
	return normalized[start : start+end]
}
