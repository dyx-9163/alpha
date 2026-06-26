package appcatalog

import (
	"sort"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

type Definition struct {
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
	Topologies            []registry.Topology
}

type Item struct {
	Name                  string              `json:"name"`
	Title                 string              `json:"title"`
	Icon                  string              `json:"icon"`
	Category              string              `json:"category"`
	CategoryLabel         string              `json:"categoryLabel"`
	SourceLabel           string              `json:"sourceLabel"`
	FallbackVersion       string              `json:"fallbackVersion"`
	Description           string              `json:"description"`
	InstallName           string              `json:"installName"`
	ResourceApp           string              `json:"resourceApp"`
	RequiresServer        bool                `json:"requiresServer"`
	BackendReady          bool                `json:"backendReady"`
	FrontendReady         bool                `json:"frontendReady"`
	RequiredResourceParts []string            `json:"requiredResourceParts"`
	Topologies            []registry.Topology `json:"topologies"`
	Versions              []string            `json:"versions"`
	Resources             []store.Resource    `json:"resources"`
	Parts                 map[string]bool     `json:"parts"`
	Deployable            bool                `json:"deployable"`
	Missing               []string            `json:"missing"`
}

func DefinitionFromManifest(manifest registry.Manifest) Definition {
	return Definition{
		Name:                  manifest.Name,
		Title:                 manifest.Title,
		Icon:                  manifest.Icon,
		Category:              manifest.Category,
		CategoryLabel:         manifest.CategoryLabel,
		SourceLabel:           manifest.SourceLabel,
		FallbackVersion:       manifest.FallbackVersion,
		Description:           manifest.Description,
		InstallName:           manifest.InstallName,
		ResourceApp:           manifest.ResourceApp,
		RequiresServer:        manifest.RequiresServer,
		BackendReady:          manifest.BackendReady,
		RequiredResourceParts: manifest.RequiredResourceParts,
		Topologies:            manifest.Topologies,
	}
}

func Registered() []Definition {
	return []Definition{}
}

func Find(name string) (Definition, bool) {
	return Definition{}, false
}

func Build(resources []store.Resource) map[string]Item {
	return BuildWithModules(resources, nil, "")
}

func BuildWithLanguage(resources []store.Resource, lang string) map[string]Item {
	return BuildWithModules(resources, nil, lang)
}

func BuildWithModules(resources []store.Resource, modules []registry.Module, lang string) map[string]Item {
	out := map[string]Item{}
	for _, module := range modules {
		def := DefinitionFromManifest(module.Manifest(lang))
		item := Item{
			Name:                  def.Name,
			Title:                 def.Title,
			Icon:                  def.Icon,
			Category:              def.Category,
			CategoryLabel:         def.CategoryLabel,
			SourceLabel:           def.SourceLabel,
			FallbackVersion:       def.FallbackVersion,
			Description:           def.Description,
			InstallName:           installName(def),
			ResourceApp:           resourceApp(def),
			RequiresServer:        def.RequiresServer,
			BackendReady:          def.BackendReady,
			FrontendReady:         false,
			RequiredResourceParts: requiredParts(def),
			Topologies:            def.Topologies,
			Versions:              []string{},
			Resources:             []store.Resource{},
			Parts:                 map[string]bool{},
			Missing:               []string{},
		}
		for _, res := range resources {
			if res.App != item.ResourceApp {
				continue
			}
			item.Resources = append(item.Resources, res)
			item.Versions = appendStringUnique(item.Versions, res.Version)
			if res.Part != "" {
				item.Parts[res.Part] = true
			}
		}
		sort.Strings(item.Versions)
		if !item.BackendReady {
			item.Missing = append(item.Missing, "backend code")
		}
		if len(item.Resources) == 0 {
			item.Missing = append(item.Missing, "offline resource")
		}
		for _, part := range item.RequiredResourceParts {
			if !item.Parts[part] {
				item.Missing = append(item.Missing, part+" resource")
			}
		}
		item.Deployable = item.BackendReady && len(item.Missing) == 0
		out[item.Name] = item
	}
	return out
}

func ResolveResources(def Definition, resources []store.Resource, version string) (string, []store.Resource) {
	app := resourceApp(def)
	var versions []string
	for _, res := range resources {
		if res.App == app {
			versions = appendStringUnique(versions, res.Version)
		}
	}
	sort.Strings(versions)
	selected := version
	if selected == "" || selected == "latest" {
		if len(versions) == 0 {
			return selected, nil
		}
		selected = versions[len(versions)-1]
	}
	var out []store.Resource
	for _, res := range resources {
		if res.App == app && res.Version == selected {
			out = append(out, res)
		}
	}
	return selected, out
}

func MissingForInstall(def Definition, resources []store.Resource, version string) []string {
	_, matched := ResolveResources(def, resources, version)
	parts := map[string]bool{}
	for _, res := range matched {
		if res.Part != "" {
			parts[res.Part] = true
		}
	}
	var missing []string
	if !def.BackendReady {
		missing = append(missing, "backend code")
	}
	if len(matched) == 0 {
		missing = append(missing, "offline resource")
	}
	for _, part := range requiredParts(def) {
		if !parts[part] {
			missing = append(missing, part+" resource")
		}
	}
	return missing
}

func installName(def Definition) string {
	if def.InstallName != "" {
		return def.InstallName
	}
	return def.Name
}

func resourceApp(def Definition) string {
	if def.ResourceApp != "" {
		return def.ResourceApp
	}
	return installName(def)
}

func requiredParts(def Definition) []string {
	if len(def.RequiredResourceParts) > 0 {
		return def.RequiredResourceParts
	}
	return []string{"backend"}
}

func appendStringUnique(in []string, value string) []string {
	for _, existing := range in {
		if existing == value {
			return in
		}
	}
	return append(in, value)
}
