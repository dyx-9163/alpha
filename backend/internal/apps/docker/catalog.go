package docker

type CatalogDefinition struct {
	Name                  string
	Title                 string
	Icon                  string
	Category              string
	CategoryLabel         string
	SourceLabel           string
	FallbackVersion       string
	Description           string
	InstallName           string
	ResourceApp           string
	RequiresServer        bool
	BackendReady          bool
	RequiredResourceParts []string
}

func Definition(lang string) CatalogDefinition {
	copy := CopyFor(lang)
	return CatalogDefinition{
		Name:                  "docker",
		Title:                 copy.Title,
		Icon:                  "D",
		Category:              "devops",
		CategoryLabel:         copy.CategoryLabel,
		SourceLabel:           copy.SourceLabel,
		FallbackVersion:       "stable",
		Description:           copy.Description,
		InstallName:           "docker",
		ResourceApp:           "docker",
		RequiresServer:        true,
		BackendReady:          true,
		RequiredResourceParts: []string{"backend"},
	}
}
