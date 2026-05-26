package skills

// DefaultLayers returns the opinionated layered discovery config for
// the Tether / Nanite portfolio: ~/.tether/skills as the lowest tier,
// ~/.nanite/skills as the next tier. Apps that want a project-local
// override layer append it to the returned slice:
//
//	layers := append(skills.DefaultLayers(), skills.Layer{
//	    Name: "project-local",
//	    Root: filepath.Join(projectRoot, ".nanite", "skills"),
//	})
//
// Order is lowest-priority first; later layers override earlier
// ones (see Discover). Missing roots are silently skipped by
// default; operators flip DiscoveryConfig.StrictMissingRoot for
// fail-loud behaviour.
//
// The legacy roots in this slice are intentionally bare strings.
// Discover expands the leading ~ via os.UserHomeDir at walk time, so
// the same slice is correct on any host.
func DefaultLayers() []Layer {
	return []Layer{
		{Name: "tether-user", Root: "~/.tether/skills"},
		{Name: "nanite-user", Root: "~/.nanite/skills"},
	}
}

// WithTetherCatalogLayer returns a Layer that points at a Tether
// catalog skills directory. Catalogs are typically distributed at
// e.g. ~/.tether/catalog/skills or /usr/local/share/tether/skills
// and have a stable Layer.Name of "tether-catalog" so provenance is
// uniform across hosts.
func WithTetherCatalogLayer(catalogRoot string) Layer {
	return Layer{
		Name: "tether-catalog",
		Root: catalogRoot,
	}
}
