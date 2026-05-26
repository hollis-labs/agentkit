// Package launcher hosts the Compile and Prepare entry points for the
// agent-launch pipeline. The Compile step turns a declarative LaunchPlan
// into a CompiledLaunch (validated, normalized, with absolute paths +
// bootdir intent resolved); the Prepare step materializes that into a
// PreparedLaunch (bootdir on disk, env / argv finalized, hooks fired).
//
// # Why a subpackage and not top-level agentlaunch
//
// The matrix subpackage (which Compile and Prepare consult to validate
// the provider × runtime pair) already imports agentlaunch for the
// shared type vocabulary (RuntimeKind, ProviderSpec). Hosting Compile /
// Prepare at the top of the agentlaunch package would create an
// import cycle: agentlaunch → matrix → agentlaunch. The launcher
// subpackage breaks the cycle by sitting BELOW both agentlaunch and
// agentlaunch/matrix in the import graph.
//
// Callers reach these entry points as launcher.Compile / launcher.Prepare;
// the types they work with (LaunchPlan, CompiledLaunch, PreparedLaunch,
// and the hook types) stay in the agentlaunch top-level package so a
// caller can declare LaunchPlan values without importing launcher.
package launcher

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/matrix"
)

// ErrHeadlessClaudeNeedsPermission is returned by Compile when a claude
// launch runs in a non-interactive mode (background / ephemeral) with an
// empty Provider.Permission. Such a launch would boot in claude's
// interactive `default` permission mode and hang on the first approval
// prompt with no human attached — Compile rejects it up front rather than
// producing a launch that is structurally guaranteed to hang. Callers can
// branch on it (errors.Is) to mark the task blocked-on-misconfiguration.
var ErrHeadlessClaudeNeedsPermission = errors.New("agentlaunch/compile: headless claude launch requires Provider.Permission")

// CompileOptions tunes the Compile entry point. The zero value is the
// production default: time.Now for the compile timestamp, empty source
// catalog identifiers.
//
// Construct option values with WithNow / WithSourceCatalog rather than
// initialising the struct directly so future fields can be added without
// breaking call sites.
type CompileOptions struct {
	// Now returns the wall-clock instant stamped into
	// Provenance.CompiledAt. Tests pin this via WithNow to make
	// compilation deterministic; production callers leave it nil so the
	// default (time.Now().UTC()) applies.
	Now func() time.Time

	// SourceCatalog is the absolute path of the catalog YAML the
	// LaunchPlan originated from, threaded through to
	// Provenance.SourceCatalog. Empty for inline / programmatic plans.
	SourceCatalog string

	// SourceCatalogVersion is the catalog's self-reported version
	// (typically a git SHA or semver), threaded through to
	// Provenance.SourceCatalogVersion. Opaque to this package.
	SourceCatalogVersion string
}

// CompileOption mutates a CompileOptions value. Returned by the
// option-builders below.
type CompileOption func(*CompileOptions)

// WithNow overrides the wall-clock function Compile uses to stamp
// Provenance.CompiledAt. Tests use this to pin time and make compilation
// deterministic; production callers should not need it.
func WithNow(fn func() time.Time) CompileOption {
	return func(o *CompileOptions) { o.Now = fn }
}

// WithSourceCatalog supplies the catalog provenance threaded into
// Provenance.SourceCatalog / Provenance.SourceCatalogVersion. Either
// argument may be empty.
func WithSourceCatalog(name, version string) CompileOption {
	return func(o *CompileOptions) {
		o.SourceCatalog = name
		o.SourceCatalogVersion = version
	}
}

// Compile turns a declarative LaunchPlan into a CompiledLaunch:
// validated, normalized, with absolute paths resolved and the bootdir
// intent populated from the provider × runtime matrix. No filesystem
// writes happen here — the compiled state is purely a function of the
// input plan, the library version, and the supplied options.
//
// # Determinism
//
// Given identical inputs (plan + options), Compile produces an
// identical CompiledLaunch — including the Provenance.PlanHash. Tests
// pin time via WithNow so even Provenance.CompiledAt is reproducible.
//
// # Error handling
//
// Errors from plan validation, matrix lookup, and path expansion are
// wrapped with "agentlaunch/compile: " prefixes so callers can locate
// the failing layer; the underlying sentinel remains errors.Is-matchable.
//
// # BootDirIntent defaults
//
// The compiler maps each provider's bootdir-renderer key to a small set
// of app-neutral relative-path defaults that reflect the layout each
// provider's CLI already expects:
//
//   - claude   → agentrc.yaml + .mcp.json (no transient boot file; the
//     boot body is concatenated into the agentrc body by go-providers'
//     Claude planter, with .mcp.json next to it).
//   - codex    → config.toml + .mcp.json (codex CLI reads its top-level
//     config from config.toml; the MCP descriptor lives alongside).
//   - opencode → OPENCODE.md (transient) + .mcp.json (opencode reads its
//     per-session prompt from OPENCODE.md; the MCP descriptor is shared).
//
// Phase 3 hook implementations may override these defaults; the
// compiler's job is to provide a sensible starting layout the preparer
// can plant against.
func Compile(ctx context.Context, plan agentlaunch.LaunchPlan, opts ...CompileOption) (*agentlaunch.CompiledLaunch, error) {
	_ = ctx // reserved for future use (cancellation while doing IO-free work is unnecessary today)
	options := CompileOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("agentlaunch/compile: %w", err)
	}

	desc, err := matrix.Lookup(plan.Provider, plan.Runtime)
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/compile: %w", err)
	}

	// Fail-fast: a headless claude launch with no permission posture boots
	// in claude's interactive `default` mode and hangs on the first tool
	// approval prompt — there is no one to answer it. Reject it here rather
	// than emit a CompiledLaunch that is structurally guaranteed to hang.
	// codex is exempt — go-providers defaults an empty approval_policy to
	// `never`; an `interactive` launch is exempt — a human can answer.
	if desc.BootDirRenderer == matrix.BootDirRendererClaude &&
		plan.Mode != agentlaunch.LaunchInteractive &&
		plan.Provider.Permission == "" {
		return nil, fmt.Errorf("%w (launch mode %q): set Provider.Permission to acceptEdits / plan / bypassPermissions",
			ErrHeadlessClaudeNeedsPermission, plan.Mode)
	}

	resolved, err := agentlaunch.ResolvePlanPaths(plan)
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/compile: %w", err)
	}

	hash, err := agentlaunch.HashLaunchPlan(resolved)
	if err != nil {
		return nil, fmt.Errorf("agentlaunch/compile: %w", err)
	}

	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	intent := bootDirIntentFor(desc.BootDirRenderer)

	compiled := &agentlaunch.CompiledLaunch{
		Plan:          &resolved,
		BootDirIntent: intent,
		Provenance: agentlaunch.Provenance{
			CompiledAt:           now(),
			SourceCatalog:        options.SourceCatalog,
			SourceCatalogVersion: options.SourceCatalogVersion,
			CompilerVersion:      agentlaunch.Version,
			PlanHash:             hash,
		},
	}

	// Resolve the provider binary at compile-time only when an override
	// is set — PATH resolution happens at prepare-time, but the matrix
	// already gives us the default binary name and we can record what
	// the caller requested.
	if plan.Provider.Binary != "" {
		compiled.ResolvedProviderBinary = plan.Provider.Binary
	} else if desc.Caps.BinaryRequired {
		compiled.ResolvedProviderBinary = desc.BinaryName
	}

	if plan.Project.Root != "" {
		compiled.ResolvedProjectRoot = resolved.Project.Root
	}

	if err := compiled.Validate(); err != nil {
		return nil, fmt.Errorf("agentlaunch/compile: %w", err)
	}

	return compiled, nil
}

// bootDirIntentFor returns the app-neutral default bootdir layout the
// compiler attaches to a CompiledLaunch given the matrix's
// BootDirRenderer key. See Compile's godoc for the per-provider
// rationale.
func bootDirIntentFor(r matrix.BootDirRenderer) agentlaunch.BootDirIntent {
	switch r {
	case matrix.BootDirRendererClaude:
		return agentlaunch.BootDirIntent{
			PerProviderBootFile: "agentrc.yaml",
			TransientBootFile:   "",
			MCPDescriptorFile:   ".mcp.json",
		}
	case matrix.BootDirRendererCodex:
		return agentlaunch.BootDirIntent{
			PerProviderBootFile: "config.toml",
			TransientBootFile:   "",
			MCPDescriptorFile:   ".mcp.json",
		}
	case matrix.BootDirRendererOpencode:
		return agentlaunch.BootDirIntent{
			PerProviderBootFile: "",
			TransientBootFile:   "OPENCODE.md",
			MCPDescriptorFile:   ".mcp.json",
		}
	default:
		return agentlaunch.BootDirIntent{}
	}
}
