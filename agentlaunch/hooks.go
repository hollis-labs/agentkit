package agentlaunch

import "context"

// BootDirHook plants files into the given directory. It is called with
// the absolute bootdir path and the compiled launch; it is expected to
// write per-provider boot files (e.g. agentrc.yaml, .mcp.json) before
// Prepare returns.
//
// The default behaviour (no hook registered) leaves the materialized
// bootdir empty — the preparer creates the directory but writes no
// files. Phase 2 / Phase 3 of SP-20260514-0003 supply real
// implementations backed by go-providers' planters.
type BootDirHook func(ctx context.Context, bootDir string, compiled *CompiledLaunch) error

// ContextHook assembles mechanical context (CLAUDE.md / project files /
// memory pointers) for the spawned session. It may write files into
// bootDir AND/OR return a boot-prompt string that the preparer injects
// into PreparedLaunch.BootPrompt. A non-empty return value overrides
// any inline boot prompt the compiled launch carried.
//
// The default behaviour (no hook registered) returns the empty string
// and writes nothing.
type ContextHook func(ctx context.Context, bootDir string, compiled *CompiledLaunch) (bootPrompt string, err error)

// WorkspaceHook is invoked after the workspace dir is resolved, before
// the bootdir is finalized. Tools that need to seed the workspace
// (e.g. git clone, symlink a sibling dependency repo) implement this.
//
// The default behaviour (no hook registered) is a no-op.
type WorkspaceHook func(ctx context.Context, workspaceDir string, compiled *CompiledLaunch) error
