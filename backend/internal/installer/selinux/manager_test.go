package selinux

import (
	"strings"
	"testing"
	"text/template"
)

func TestServiceAccessHelpersExposePolicyFunctions(t *testing.T) {
	script := ServiceAccessHelpers()
	for _, want := range []string{
		"open_firewall_ports()",
		"allow_selinux_ports()",
		"set_selinux_fcontext()",
		"restore_selinux_context()",
		"set_selinux_boolean()",
		"print_recent_selinux_denials()",
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
