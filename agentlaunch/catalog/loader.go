package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Sentinel errors returned by Load* and Resolve.
var (
	// ErrLaunchNotFound is returned by GlobalCatalog.Resolve when the
	// requested launch ID is not present in GlobalCatalog.Launches.
	ErrLaunchNotFound = errors.New("agentlaunch/catalog: launch id not found")

	// ErrProjectNotFound is returned when a launch references a
	// project ID that is not present in GlobalCatalog.Projects.
	ErrProjectNotFound = errors.New("agentlaunch/catalog: project id not found")

	// ErrAgentNotFound is returned when a launch references an agent
	// ID that is not present in GlobalCatalog.Agents.
	ErrAgentNotFound = errors.New("agentlaunch/catalog: agent id not found")

	// ErrProviderNotFound is returned when a launch references a
	// provider ID that is not present in GlobalCatalog.Providers.
	ErrProviderNotFound = errors.New("agentlaunch/catalog: provider id not found")

	// ErrUnsupportedRuntime is returned when a provider declares a
	// runtime_kind value that does not map cleanly onto an
	// agentlaunch.RuntimeKind. The original value is captured in the
	// wrapped error and in the launch plan's annotations.
	ErrUnsupportedRuntime = errors.New("agentlaunch/catalog: unsupported runtime kind")

	// ErrMissingLaunchID is returned by LoadLaunch when the parsed
	// launches/<id>.yaml has an empty `id:` field.
	ErrMissingLaunchID = errors.New("agentlaunch/catalog: launch profile missing id")

	// ErrMissingBootProfileID is returned by LoadBootProfile when the
	// parsed boot-profiles/<id>.yaml has an empty `id:` field.
	ErrMissingBootProfileID = errors.New("agentlaunch/catalog: boot profile missing id")
)

// LoadGlobal reads a Tether-style catalog from path and returns the
// populated GlobalCatalog. The path may be either:
//
//   - a directory containing global.yaml plus the sibling subdirectories
//     declared in `catalog.roots` (projects/, agents/, providers/,
//     launches/, boot-profiles/); or
//   - the global.yaml file itself, in which case the loader walks the
//     directory containing that file just as if a directory had been
//     supplied; or
//   - a self-contained YAML file that carries inline lists on
//     GlobalCatalog directly (no sibling subdirectories required).
//
// When the file path resolves to a single self-contained YAML the
// loader returns the parsed shape as-is without walking the parent
// directory.
func LoadGlobal(path string) (*GlobalCatalog, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/catalog: stat %s: %w", path, err)
	}
	if info.IsDir() {
		return loadGlobalFromDir(path)
	}
	// Path is a regular file. Parse it. If it carries no inline lists
	// AND its parent contains the expected sibling subdirectories, fall
	// through to a directory-walk anchored at the parent.
	b, err := os.ReadFile(path) //nolint:gosec // catalog-sourced path
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/catalog: read %s: %w", path, err)
	}
	g, err := decodeGlobal(b)
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/catalog: parse %s: %w", path, err)
	}
	hasInline := len(g.Projects)+len(g.Agents)+len(g.Providers)+len(g.Launches) > 0
	if hasInline {
		return g, nil
	}
	// No inline entries — treat as a Tether-style global.yaml pointing
	// at sibling directories and walk them.
	return enrichGlobalFromDir(g, filepath.Dir(path))
}

// LoadGlobalFromBytes parses an inline GlobalCatalog YAML from b. Used
// by tests and callers that already have the bytes in hand. The catalog
// must carry inline Projects/Agents/Providers/Launches lists — no
// directory walk is performed.
func LoadGlobalFromBytes(b []byte) (*GlobalCatalog, error) {
	g, err := decodeGlobal(b)
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/catalog: parse bytes: %w", err)
	}
	return g, nil
}

func decodeGlobal(b []byte) (*GlobalCatalog, error) {
	var g GlobalCatalog
	if err := yaml.Unmarshal(b, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

// loadGlobalFromDir walks a Tether-style catalog directory: it parses
// global.yaml at the top, then enumerates the sibling subdirectories
// declared by catalog.roots (with sensible defaults for the unset
// subdirs) and appends each parsed entry to the corresponding inline
// slice on the returned GlobalCatalog.
func loadGlobalFromDir(dir string) (*GlobalCatalog, error) {
	globalPath := filepath.Join(dir, "global.yaml")
	b, err := os.ReadFile(globalPath) //nolint:gosec // catalog-sourced path
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/catalog: read %s: %w", globalPath, err)
	}
	g, err := decodeGlobal(b)
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/catalog: parse %s: %w", globalPath, err)
	}
	return enrichGlobalFromDir(g, dir)
}

// enrichGlobalFromDir walks the sibling subdirectories named in
// g.Catalog.Roots (falling back to well-known defaults for unset
// fields) and appends every parsed entry to g's inline slices.
// Per-file parse errors are wrapped with the file path so authors can
// pinpoint the offending fixture.
func enrichGlobalFromDir(g *GlobalCatalog, dir string) (*GlobalCatalog, error) {
	projectsDir := chooseRoot(dir, g.Catalog.Roots.Projects, "projects")
	agentsDir := chooseRoot(dir, g.Catalog.Roots.Agents, "agents")
	providersDir := chooseRoot(dir, g.Catalog.Roots.Providers, "providers")
	launchesDir := chooseRoot(dir, g.Catalog.Roots.Launches, "launches")

	if err := walkYAML(projectsDir, func(path string, b []byte) error {
		var p ProjectEntry
		if err := yaml.Unmarshal(b, &p); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		g.Projects = append(g.Projects, p)
		return nil
	}); err != nil {
		return nil, err
	}

	if err := walkYAML(agentsDir, func(path string, b []byte) error {
		var a AgentEntry
		if err := yaml.Unmarshal(b, &a); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		g.Agents = append(g.Agents, a)
		return nil
	}); err != nil {
		return nil, err
	}

	if err := walkYAML(providersDir, func(path string, b []byte) error {
		var p ProviderEntry
		if err := yaml.Unmarshal(b, &p); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		g.Providers = append(g.Providers, p)
		return nil
	}); err != nil {
		return nil, err
	}

	if err := walkYAML(launchesDir, func(path string, b []byte) error {
		var l LaunchProfile
		if err := yaml.Unmarshal(b, &l); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		g.Launches = append(g.Launches, l)
		return nil
	}); err != nil {
		return nil, err
	}

	return g, nil
}

// chooseRoot picks the absolute directory to walk for a given root
// kind. fromGlobal is the value the global.yaml declared (may be empty
// or relative); fallback is the well-known default. Absolute paths or
// paths starting with "~" are returned verbatim; relative paths are
// joined against dir.
func chooseRoot(dir, fromGlobal, fallback string) string {
	if fromGlobal != "" {
		if filepath.IsAbs(fromGlobal) || len(fromGlobal) > 0 && fromGlobal[0] == '~' {
			return fromGlobal
		}
		return filepath.Join(dir, fromGlobal)
	}
	return filepath.Join(dir, fallback)
}

// walkYAML calls fn for every *.yaml / *.yml file in dir
// non-recursively. A missing dir is not an error — the catalog is
// allowed to omit any subdir entirely.
func walkYAML(dir string, fn func(path string, b []byte) error) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("agentlaunch/catalog: read dir %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path) //nolint:gosec // catalog-sourced path
		if err != nil {
			return fmt.Errorf("agentlaunch/catalog: read %s: %w", path, err)
		}
		if err := fn(path, b); err != nil {
			return fmt.Errorf("agentlaunch/catalog: %w", err)
		}
	}
	return nil
}

// LoadLaunch reads one launches/<id>.yaml file and returns the parsed
// LaunchProfile. The path may name any *.yaml file; the loader does
// not enforce a specific directory layout.
func LoadLaunch(path string) (*LaunchProfile, error) {
	b, err := os.ReadFile(path) //nolint:gosec // catalog-sourced path
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/catalog: read %s: %w", path, err)
	}
	lp, err := LoadLaunchFromBytes(b)
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/catalog: parse %s: %w", path, err)
	}
	return lp, nil
}

// LoadLaunchFromBytes parses one launch profile from b.
func LoadLaunchFromBytes(b []byte) (*LaunchProfile, error) {
	var lp LaunchProfile
	if err := yaml.Unmarshal(b, &lp); err != nil {
		return nil, err
	}
	if lp.ID == "" {
		return nil, ErrMissingLaunchID
	}
	return &lp, nil
}

// LoadBootProfile reads one boot-profiles/<id>.yaml file and returns
// the parsed BootProfile.
func LoadBootProfile(path string) (*BootProfile, error) {
	b, err := os.ReadFile(path) //nolint:gosec // catalog-sourced path
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/catalog: read %s: %w", path, err)
	}
	bp, err := LoadBootProfileFromBytes(b)
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/catalog: parse %s: %w", path, err)
	}
	return bp, nil
}

// LoadBootProfileFromBytes parses one boot profile from b.
func LoadBootProfileFromBytes(b []byte) (*BootProfile, error) {
	var bp BootProfile
	if err := yaml.Unmarshal(b, &bp); err != nil {
		return nil, err
	}
	if bp.ID == "" {
		return nil, ErrMissingBootProfileID
	}
	return &bp, nil
}
