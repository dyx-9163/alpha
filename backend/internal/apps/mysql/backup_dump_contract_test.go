package mysql

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDumpVerificationHelperRequiresCompleteMySQLShell8036Catalog(t *testing.T) {
	python := testPythonInterpreter(t)

	tests := []struct {
		name    string
		fixture map[string]string
		valid   bool
		tables  int
	}{
		{name: "valid completion accepts zero and uint64 max byte counts", fixture: validMySQLShell8036DumpFixture(), valid: true, tables: 1},
		{name: "valid business schema may contain zero base tables", fixture: emptySchemaMySQLShell8036DumpFixture(), valid: true, tables: 0},
		{name: "empty completion marker", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.done.json", `{}`)},
		{name: "null completion marker", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.done.json", `null`)},
		{name: "empty completion end", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.done.json", `{"end":"","dataBytes":0,"tableDataBytes":{"aifar_business":{"orders":0}},"chunkFileBytes":{"aifar_business@orders.tsv.zst":0}}`)},
		{name: "malformed completion marker", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.done.json", `{"end":"done"} trailing`)},
		{name: "negative byte count", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.done.json", `{"end":"done","dataBytes":-1,"tableDataBytes":{"aifar_business":{"orders":0}},"chunkFileBytes":{"aifar_business@orders.tsv.zst":0}}`)},
		{name: "fractional byte count", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.done.json", `{"end":"done","dataBytes":0,"tableDataBytes":{"aifar_business":{"orders":0.5}},"chunkFileBytes":{"aifar_business@orders.tsv.zst":0}}`)},
		{name: "overflowing byte count", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.done.json", `{"end":"done","dataBytes":18446744073709551616,"tableDataBytes":{"aifar_business":{"orders":0}},"chunkFileBytes":{"aifar_business@orders.tsv.zst":0}}`)},
		{name: "wrong dump metadata version", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.json", `{"version":"2.0.0","origin":"dumpInstance","consistent":true,"schemas":["aifar_business"],"basenames":{"aifar_business":"aifar_business"}}`)},
		{name: "wrong dump metadata origin", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.json", `{"version":"2.0.1","origin":"dumpSchemas","consistent":true,"schemas":["aifar_business"],"basenames":{"aifar_business":"aifar_business"}}`)},
		{name: "inconsistent dump metadata", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.json", `{"version":"2.0.1","origin":"dumpInstance","consistent":false,"schemas":["aifar_business"],"basenames":{"aifar_business":"aifar_business"}}`)},
		{name: "duplicate top level schema", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.json", `{"version":"2.0.1","origin":"dumpInstance","consistent":true,"schemas":["aifar_business","aifar_business"],"basenames":{"aifar_business":"aifar_business"}}`)},
		{name: "ambiguous top level basenames", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.json", `{"version":"2.0.1","origin":"dumpInstance","consistent":true,"schemas":["aifar_business","billing"],"basenames":{"aifar_business":"shared","billing":"shared"}}`)},
		{name: "system schema is not a business schema", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.json", `{"version":"2.0.1","origin":"dumpInstance","consistent":true,"schemas":["mysql"],"basenames":{"mysql":"mysql"}}`)},
		{name: "missing table metadata", fixture: deleteDumpFixture(validMySQLShell8036DumpFixture(), "aifar_business@orders.json")},
		{name: "extra table metadata", fixture: addExtraTableMetadata(validMySQLShell8036DumpFixture())},
		{name: "extra malformed root control metadata", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@junk.json", `null trailing`)},
		{name: "duplicate table catalog entry", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "aifar_business.json", `{"schema":"aifar_business","includesDdl":true,"includesViewsDdl":true,"includesData":true,"tables":["orders","orders"],"views":["orders_view"],"basenames":{"orders":"aifar_business@orders","orders_view":"aifar_business@orders_view"}}`)},
		{name: "table and view catalogs overlap", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "aifar_business.json", `{"schema":"aifar_business","includesDdl":true,"includesViewsDdl":true,"includesData":true,"tables":["orders"],"views":["orders"],"basenames":{"orders":"aifar_business@orders"}}`)},
		{name: "schema metadata back reference mismatch", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "aifar_business.json", `{"schema":"other","includesDdl":true,"includesViewsDdl":true,"includesData":true,"tables":["orders"],"views":["orders_view"],"basenames":{"orders":"aifar_business@orders","orders_view":"aifar_business@orders_view"}}`)},
		{name: "schema metadata incomplete", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "aifar_business.json", `{"schema":"aifar_business","includesDdl":true,"includesViewsDdl":false,"includesData":true,"tables":["orders"],"views":["orders_view"],"basenames":{"orders":"aifar_business@orders","orders_view":"aifar_business@orders_view"}}`)},
		{name: "ambiguous table basenames", fixture: ambiguousTableBasenamesFixture()},
		{name: "table metadata back reference mismatch", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "aifar_business@orders.json", `{"options":{"schema":"aifar_business","table":"other","columns":["id"]},"includesData":true,"includesDdl":true,"extension":"tsv.zst","compression":"zstd","primaryIndex":["id"]}`)},
		{name: "table metadata columns are not strings", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "aifar_business@orders.json", `{"options":{"schema":"aifar_business","table":"orders","columns":[1]},"includesData":true,"includesDdl":true,"extension":"tsv.zst","compression":"zstd","primaryIndex":["id"]}`)},
		{name: "table metadata primary index is not an array", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "aifar_business@orders.json", `{"options":{"schema":"aifar_business","table":"orders","columns":["id"]},"includesData":true,"includesDdl":true,"extension":"tsv.zst","compression":"zstd","primaryIndex":"id"}`)},
		{name: "table metadata compression contract mismatch", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "aifar_business@orders.json", `{"options":{"schema":"aifar_business","table":"orders","columns":["id"]},"includesData":true,"includesDdl":true,"extension":"tsv","compression":"none","primaryIndex":["id"]}`)},
		{name: "duplicate completion object key", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.done.json", `{"end":"done","dataBytes":0,"tableDataBytes":{"aifar_business":{"orders":0,"orders":0}},"chunkFileBytes":{"aifar_business@orders.tsv.zst":0}}`)},
		{name: "missing tableDataBytes catalog item", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.done.json", `{"end":"done","dataBytes":0,"tableDataBytes":{},"chunkFileBytes":{"aifar_business@orders.tsv.zst":0}}`)},
		{name: "extra tableDataBytes catalog item", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.done.json", `{"end":"done","dataBytes":0,"tableDataBytes":{"aifar_business":{"orders":0,"extra":0}},"chunkFileBytes":{"aifar_business@orders.tsv.zst":0}}`)},
		{name: "missing chunkFileBytes inventory item", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.done.json", `{"end":"done","dataBytes":0,"tableDataBytes":{"aifar_business":{"orders":0}},"chunkFileBytes":{}}`)},
		{name: "extra chunkFileBytes inventory item", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "@.done.json", `{"end":"done","dataBytes":0,"tableDataBytes":{"aifar_business":{"orders":0}},"chunkFileBytes":{"aifar_business@orders.tsv.zst":0,"missing.tsv.zst":0}}`)},
		{name: "unreported data file in inventory", fixture: mutateDumpFixture(validMySQLShell8036DumpFixture(), "unreported.tsv.zst", "")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			work := writeMySQLShell8036DumpFixture(t, test.fixture)
			command := exec.Command(python, "-c", dumpVerificationHelper, work)
			output, err := command.CombinedOutput()
			if !test.valid {
				if err == nil {
					t.Fatalf("invalid dump accepted: %s", output)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid dump rejected: %v: %s", err, output)
			}
			verification, err := parseDumpVerification(string(output), []string{"aifar_business"})
			if err != nil {
				t.Fatalf("verification output rejected: %v: %s", err, output)
			}
			if verification.SchemaCount != 1 || verification.TableCount != test.tables || len(verification.Schemas) != 1 || len(verification.Schemas[0].Tables) != test.tables {
				t.Fatalf("verification catalog = %+v", verification)
			}
		})
	}
}

func testPythonInterpreter(t *testing.T) string {
	t.Helper()
	candidates := []string{os.Getenv("AIFAR_TEST_PYTHON"), "python3", "python"}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		resolved, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		if err := exec.Command(resolved, "--version").Run(); err == nil {
			return resolved
		}
	}
	t.Skip("Python 3 is required to execute the production dump verification helper")
	return ""
}

func validMySQLShell8036DumpFixture() map[string]string {
	return map[string]string{
		"@.json":                        `{"version":"2.0.1","origin":"dumpInstance","consistent":true,"schemas":["aifar_business"],"basenames":{"aifar_business":"aifar_business"}}`,
		"@.done.json":                   `{"end":"2026-07-28 10:00:00","dataBytes":18446744073709551615,"tableDataBytes":{"aifar_business":{"orders":0}},"chunkFileBytes":{"aifar_business@orders.tsv.zst":18446744073709551615}}`,
		"aifar_business.json":           `{"schema":"aifar_business","includesDdl":true,"includesViewsDdl":true,"includesData":true,"tables":["orders"],"views":["orders_view"],"basenames":{"orders":"aifar_business@orders","orders_view":"aifar_business@orders_view"}}`,
		"aifar_business@orders.json":    `{"options":{"schema":"aifar_business","table":"orders","columns":["id"]},"includesData":true,"includesDdl":true,"extension":"tsv.zst","compression":"zstd","primaryIndex":["id"]}`,
		"aifar_business@orders.tsv.zst": "",
	}
}

func emptySchemaMySQLShell8036DumpFixture() map[string]string {
	return map[string]string{
		"@.json":              `{"version":"2.0.1","origin":"dumpInstance","consistent":true,"schemas":["aifar_business"],"basenames":{"aifar_business":"aifar_business"}}`,
		"@.done.json":         `{"end":"2026-07-28 10:00:00","dataBytes":0,"tableDataBytes":{},"chunkFileBytes":{}}`,
		"aifar_business.json": `{"schema":"aifar_business","includesDdl":true,"includesViewsDdl":true,"includesData":true,"tables":[],"views":[],"basenames":{}}`,
	}
}

func ambiguousTableBasenamesFixture() map[string]string {
	fixture := validMySQLShell8036DumpFixture()
	fixture["aifar_business.json"] = `{"schema":"aifar_business","includesDdl":true,"includesViewsDdl":true,"includesData":true,"tables":["orders","customers"],"views":[],"basenames":{"orders":"aifar_business@shared","customers":"aifar_business@shared"}}`
	fixture["aifar_business@shared.json"] = `{"options":{"schema":"aifar_business","table":"orders","columns":["id"]},"includesData":true,"includesDdl":true,"extension":"tsv.zst","compression":"zstd","primaryIndex":["id"]}`
	delete(fixture, "aifar_business@orders.json")
	return fixture
}

func addExtraTableMetadata(fixture map[string]string) map[string]string {
	fixture["aifar_business@extra.json"] = `{"options":{"schema":"aifar_business","table":"extra","columns":["id"]},"includesData":true,"includesDdl":true,"extension":"tsv.zst","compression":"zstd","primaryIndex":[]}`
	return fixture
}

func mutateDumpFixture(fixture map[string]string, name, content string) map[string]string {
	fixture[name] = content
	return fixture
}

func deleteDumpFixture(fixture map[string]string, name string) map[string]string {
	delete(fixture, name)
	return fixture
}

func writeMySQLShell8036DumpFixture(t *testing.T, fixture map[string]string) string {
	t.Helper()
	work := t.TempDir()
	dump := filepath.Join(work, "dump")
	if err := os.MkdirAll(dump, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range fixture {
		if strings.ContainsAny(name, `/\\`) {
			t.Fatalf("unsafe fixture name %q", name)
		}
		if err := os.WriteFile(filepath.Join(dump, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return work
}
