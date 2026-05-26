package agentlaunch

import (
	"fmt"
	"path/filepath"
)

// resolveAbs returns the absolute form of path. Empty paths pass through
// unchanged so callers can use this as a no-op when a field is optional
// and unset. Non-empty paths are run through filepath.Abs which resolves
// against the current working directory.
//
// Errors are wrapped with a label so the caller can identify which field
// failed without re-deriving the input value.
func resolveAbs(label, path string) (string, error) {
	if path == "" {
		return "", nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("agentlaunch/pathexpand: resolve %s %q: %w", label, path, err)
	}
	return abs, nil
}

// ResolvePlanPaths populates absolute forms for every plan field that
// should be absolute when set. Empty fields stay empty. The resolved
// plan is returned by value — the input is not mutated.
//
// Exported so the launcher subpackage (which hosts Compile) can call
// it without re-implementing the abs-path discipline. End-user callers
// typically reach this through launcher.Compile rather than directly.
//
// Resolved fields:
//
//   - Project.Root
//   - Workspace.Workdir
//   - Workspace.WorkspaceDir
//   - BootProfile.CatalogPath
//
// Other path-shaped fields on the plan (Agent.RoleFile, Provider.Binary)
// are deliberately left untouched: RoleFile resolution depends on a
// catalog-defined role-root that this layer doesn't know about, and
// Binary may legitimately be a PATH-relative executable name.
func ResolvePlanPaths(plan LaunchPlan) (LaunchPlan, error) {
	var err error

	plan.Project.Root, err = resolveAbs("project.root", plan.Project.Root)
	if err != nil {
		return plan, err
	}

	plan.Workspace.Workdir, err = resolveAbs("workspace.workdir", plan.Workspace.Workdir)
	if err != nil {
		return plan, err
	}

	plan.Workspace.WorkspaceDir, err = resolveAbs("workspace.workspace_dir", plan.Workspace.WorkspaceDir)
	if err != nil {
		return plan, err
	}

	plan.BootProfile.CatalogPath, err = resolveAbs("boot_profile.catalog_path", plan.BootProfile.CatalogPath)
	if err != nil {
		return plan, err
	}

	return plan, nil
}
