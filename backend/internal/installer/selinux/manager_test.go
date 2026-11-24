package selinux

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"text/template"
)

func TestLabelSELinuxServiceTree(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires the Linux install script shell")
	}
	for _, tc := range []struct {
		name, mode                                    string
		haveChcon, rejectLabel, wantFind, wantFailure bool
	}{
		{"enforcing relabels only default entries", "Enforcing", true, false, true, false},
		{"permissive prepares for enforcement", "Permissive", true, false, true, false},
		{"disabled leaves labels alone", "Disabled", false, false, false, false},
		{"missing tool fails an enabled installation", "Enforcing", false, false, false, true},
		{"failed label change prevents service startup", "Enforcing", true, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tree := filepath.Join(dir, "application path")
			if err := os.Mkdir(tree, 0o700); err != nil {
				t.Fatal(err)
			}
			writeTool := func(name, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			writeTool("getenforce", `printf '%s\n' "$TEST_MODE"`)
			writeTool("find", `printf '%s\n' "$@" > "$TEST_RECORD"; exit "$TEST_FIND_EXIT"`)
			if tc.haveChcon {
				writeTool("chcon", "exit 0")
			}
			record := filepath.Join(dir, "find-args")
			status := "0"
			if tc.rejectLabel {
				status = "1"
			}
			cmd := exec.Command("/bin/sh", "-eu", "-c", ServiceAccessHelpers()+`label_selinux_service_tree "$1"`, "test", tree)
			cmd.Env = []string{"PATH=" + dir, "TEST_MODE=" + tc.mode, "TEST_RECORD=" + record, "TEST_FIND_EXIT=" + status}
			if output, err := cmd.CombinedOutput(); (err != nil) != tc.wantFailure {
				t.Fatalf("labeling failure = %v, output: %s", err, output)
			}
			got, err := os.ReadFile(record)
			if !tc.wantFind {
				if !os.IsNotExist(err) {
					t.Fatalf("unexpected file traversal: %s, %v", got, err)
				}
				return
			}
			want := strings.Join([]string{"-P", tree, "-xdev", "(", "-context", "*:default_t:*", "-o", "-context", "*:unlabeled_t:*", ")",
				"-exec", "chcon", "-h", "-t", "usr_t", "--", "{}", "+", ""}, "\n")
			if err != nil || string(got) != want {
				t.Fatalf("relabel command = %q, error = %v, want %q", got, err, want)
			}
		})
	}
}

func TestServiceAccessHelpersExposePolicyFunctions(t *testing.T) {
	script := ServiceAccessHelpers()
	for _, want := range []string{
		"open_firewall_ports()",
		"allow_selinux_ports()",
		"set_selinux_fcontext()",
		"restore_selinux_context()",
		"set_selinux_boolean()",
		"print_recent_selinux_denials()",
		"ensure_semanage()",
		"install_selinux_tools_from_local_rpms()",
		"install_selinux_tools_from_repo()",
		"policycoreutils-python-utils",
		"policycoreutils-python",
		"semanage port -a",
		"semanage fcontext -a",
		"restorecon -R",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("SELinux helper script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "{{") {
		t.Fatalf("helper script must not contain template markers:\n%s", script)
	}
}

func TestAddTemplateFuncsDoesNotMutateBase(t *testing.T) {
	base := template.FuncMap{"existing": func() string { return "ok" }}
	next := AddTemplateFuncs(base)
	if _, ok := base[TemplateFuncName]; ok {
		t.Fatal("AddTemplateFuncs must not mutate the input func map")
	}
	if next[TemplateFuncName] == nil || next["existing"] == nil {
		t.Fatalf("merged template funcs missing expected entries: %+v", next)
	}
}
