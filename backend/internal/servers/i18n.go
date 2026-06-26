package servers

import (
	"aifar-deployment/backend/internal/i18n"
)

type Copy struct {
	ValidateServer     string
	LoadServer         string
	CheckCredential    string
	ProbeSSH           string
	CollectRuntime     string
	CredentialMissing  string
	ProbingServer      string
	ProbeSucceeded     string
	RuntimePlaceholder string
	Saved              string
	Deleted            string
}

func CopyFor(lang string) Copy {
	return Copy{
		ValidateServer:     i18n.Text(lang, "servers.validateServer"),
		LoadServer:         i18n.Text(lang, "servers.loadServer"),
		CheckCredential:    i18n.Text(lang, "servers.checkCredential"),
		ProbeSSH:           i18n.Text(lang, "servers.probeSSH"),
		CollectRuntime:     i18n.Text(lang, "servers.collectRuntime"),
		CredentialMissing:  i18n.Text(lang, "servers.credentialMissing"),
		ProbingServer:      i18n.Text(lang, "servers.probingServer"),
		ProbeSucceeded:     i18n.Text(lang, "servers.probeSucceeded"),
		RuntimePlaceholder: i18n.Text(lang, "servers.runtimePlaceholder"),
		Saved:              i18n.Text(lang, "servers.saved"),
		Deleted:            i18n.Text(lang, "servers.deleted"),
	}
}
