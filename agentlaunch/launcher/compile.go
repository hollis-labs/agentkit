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
// shared type vocabulary (ProviderSpec). Hosting Compile /
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
	"strings"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-providers/layout"

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
// The intent is read from go-providers' layout table for the plan's runtime
// and mode, the same rows the provider projection plants, so it names what
// the harness actually reads:
//
//   - PerProviderBootFile: the instructions row (CLAUDE.md, AGENTS.md,
//     OpenCode's agents/<agent>.md).
//   - TransientBootFile: the boot row (boot.md).
//   - MCPDescriptorFile: the MCP row (.mcp.json, or agy's
//     .agents/plugins/tether/mcp_config.json).
//
// A runtime with no layout (ACP-only: Copilot, Pi) gets an empty intent.
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
	if desc.ProviderID == runtimes.Claude &&
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

	intent := bootDirIntentFor(desc, agentName(&resolved))

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

	// Record the binary: the plan's override, else the registry's binary
	// name. PATH resolution happens at prepare time.
	compiled.ResolvedProviderBinary = desc.BinaryName

	if plan.Project.Root != "" {
		compiled.ResolvedProjectRoot = resolved.Project.Root
	}

	if err := compiled.Validate(); err != nil {
		return nil, fmt.Errorf("agentlaunch/compile: %w", err)
	}

	return compiled, nil
}

// bootDirIntentFor returns the bootdir layout the compiler attaches to a
// CompiledLaunch, read from the layout table rows for the descriptor's
// runtime and mode. See Compile's godoc.
func bootDirIntentFor(desc matrix.Descriptor, agent string) agentlaunch.BootDirIntent {
	shape := layout.Shape{Mode: desc.Runtime}
	rel := func(c layout.Concern) string {
		e, ok := layout.Find(desc.ProviderID, shape, c)
		if !ok || e.Root != layout.RootBoot {
			return ""
		}
		return strings.ReplaceAll(e.Rel, layout.AgentPlaceholder, agent)
	}
	return agentlaunch.BootDirIntent{
		PerProviderBootFile: rel(layout.Instructions),
		TransientBootFile:   rel(layout.Boot),
		MCPDescriptorFile:   rel(layout.MCP),
	}
}

// agentName is the name a provider's agent file is planted under:
// AgentSpec.Name, then AgentSpec.ID (the providerplant precedence).
func agentName(plan *agentlaunch.LaunchPlan) string {
	if plan.Agent.Name != "" {
		return plan.Agent.Name
	}
	return plan.Agent.ID
}
