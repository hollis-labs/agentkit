package launcher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/matrix"
)

// fixedTime is the pinned wall-clock value tests use to make Compile's
// Provenance.CompiledAt deterministic.
var fixedTime = time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)

// validPlanForCompile returns a plan that passes Validate AND is a
// known legal (provider, runtime) pair in the matrix.
func validPlanForCompile() agentlaunch.LaunchPlan {
	return agentlaunch.LaunchPlan{
		Project: agentlaunch.ProjectSpec{ID: "proj", Name: "Project"},
		Agent:   agentlaunch.AgentSpec{ID: "agent", Name: "Agent"},
		Provider: agentlaunch.ProviderSpec{
			ID: "claude",
		},
		Runtime: agentlaunch.RuntimePTY,
		Workspace: agentlaunch.WorkspaceSpec{
			Mode:         agentlaunch.WorkspaceShared,
			WorkspaceDir: "/abs/ws",
		},
		BootProfile: agentlaunch.BootProfileRef{
			Inline: &agentlaunch.BootProfileInline{
				BootPrompt: "persona",
				BootMode:   agentlaunch.BootModePlanted,
			},
		},
		Mode: agentlaunch.LaunchInteractive,
	}
}

// TestCompileHeadlessClaudeNeedsPermission pins the fail-fast reject: a
// claude launch in a non-interactive mode with no Provider.Permission is
// refused at compile time rather than compiled into a launch that hangs.
func TestCompileHeadlessClaudeNeedsPermission(t *testing.T) {
	// background claude, no permission → rejected up front.
	p := validPlanForCompile()
	p.Mode = agentlaunch.LaunchBackground
	if _, err := Compile(context.Background(), p); !errors.Is(err, ErrHeadlessClaudeNeedsPermission) {
		t.Fatalf("background claude, empty Permission: err = %v, want ErrHeadlessClaudeNeedsPermission", err)
	}

	// background claude WITH a permission posture → accepted.
	p = validPlanForCompile()
	p.Mode = agentlaunch.LaunchBackground
	p.Provider.Permission = "acceptEdits"
	if _, err := Compile(context.Background(), p); err != nil {
		t.Errorf("background claude with Permission set: Compile = %v, want nil", err)
	}

	// interactive claude, no permission → accepted (a human answers prompts).
	p = validPlanForCompile()
	p.Mode = agentlaunch.LaunchInteractive
	if _, err := Compile(context.Background(), p); err != nil {
		t.Errorf("interactive claude, empty Permission: Compile = %v, want nil", err)
	}

	// codex is exempt — go-providers defaults an empty approval_policy to
	// `never`, so a headless codex with no permission does not hang.
	p = validPlanForCompile()
	p.Provider.ID = "codex"
	p.Runtime = agentlaunch.RuntimeSubprocess
	p.Mode = agentlaunch.LaunchBackground
	if _, err := Compile(context.Background(), p); err != nil {
		t.Errorf("background codex, empty Permission: Compile = %v, want nil (codex is exempt)", err)
	}
}

// TestCompileHappyPathDefaultsClaudePty exercises the most common path:
// claude/pty with an inline boot profile.
func TestCompileHappyPathDefaultsClaudePty(t *testing.T) {
	plan := validPlanForCompile()
	got, err := Compile(context.Background(), plan,
		WithNow(func() time.Time { return fixedTime }),
		WithSourceCatalog("/abs/catalog.yaml", "v1.2.3"),
	)
	if err != nil {
		t.Fatalf("Compile = %v", err)
	}
	if got == nil {
		t.Fatalf("Compile returned nil compiled")
	}
	if got.Plan == nil {
		t.Fatalf("Compile.Plan is nil")
	}
	if got.Provenance.CompiledAt != fixedTime {
		t.Fatalf("CompiledAt = %v, want %v", got.Provenance.CompiledAt, fixedTime)
	}
	if got.Provenance.CompilerVersion != agentlaunch.Version {
		t.Fatalf("CompilerVersion = %q, want %q", got.Provenance.CompilerVersion, agentlaunch.Version)
	}
	if got.Provenance.SourceCatalog != "/abs/catalog.yaml" {
		t.Fatalf("SourceCatalog = %q", got.Provenance.SourceCatalog)
	}
	if got.Provenance.SourceCatalogVersion != "v1.2.3" {
		t.Fatalf("SourceCatalogVersion = %q", got.Provenance.SourceCatalogVersion)
	}
	if got.Provenance.PlanHash == "" {
		t.Fatalf("PlanHash empty")
	}
	if got.ResolvedProviderBinary != "claude" {
		t.Fatalf("ResolvedProviderBinary = %q, want \"claude\"", got.ResolvedProviderBinary)
	}
}

// TestCompileBootDirIntentDefaults locks in the per-provider bootdir
// intent the brief specified. Each row uses a (provider, runtime) pair
// that the matrix accepts.
func TestCompileBootDirIntentDefaults(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		runtime  agentlaunch.RuntimeKind
		want     agentlaunch.BootDirIntent
	}{
		{
			name:     "claude/pty → agentrc.yaml + .mcp.json",
			provider: "claude",
			runtime:  agentlaunch.RuntimePTY,
			want: agentlaunch.BootDirIntent{
				PerProviderBootFile: "agentrc.yaml",
				TransientBootFile:   "",
				MCPDescriptorFile:   ".mcp.json",
			},
		},
		{
			name:     "codex/jsonrpc-stdio → config.toml + .mcp.json",
			provider: "codex",
			runtime:  agentlaunch.RuntimeJsonRpcStdio,
			want: agentlaunch.BootDirIntent{
				PerProviderBootFile: "config.toml",
				TransientBootFile:   "",
				MCPDescriptorFile:   ".mcp.json",
			},
		},
		{
			name:     "opencode/subprocess → OPENCODE.md + .mcp.json",
			provider: "opencode",
			runtime:  agentlaunch.RuntimeSubprocess,
			want: agentlaunch.BootDirIntent{
				PerProviderBootFile: "",
				TransientBootFile:   "OPENCODE.md",
				MCPDescriptorFile:   ".mcp.json",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			plan := validPlanForCompile()
			plan.Provider.ID = tc.provider
			plan.Runtime = tc.runtime
			got, err := Compile(context.Background(), plan,
				WithNow(func() time.Time { return fixedTime }),
			)
			if err != nil {
				t.Fatalf("Compile = %v", err)
			}
			if got.BootDirIntent != tc.want {
				t.Fatalf("BootDirIntent = %+v, want %+v", got.BootDirIntent, tc.want)
			}
		})
	}
}

// TestCompileDeterministic confirms that two compiles of an identical
// plan with identical options return identical PlanHash and identical
// CompiledAt timestamps.
func TestCompileDeterministic(t *testing.T) {
	plan := validPlanForCompile()
	a, err := Compile(context.Background(), plan,
		WithNow(func() time.Time { return fixedTime }),
		WithSourceCatalog("/abs/c.yaml", "v1"),
	)
	if err != nil {
		t.Fatalf("Compile a = %v", err)
	}
	b, err := Compile(context.Background(), plan,
		WithNow(func() time.Time { return fixedTime }),
		WithSourceCatalog("/abs/c.yaml", "v1"),
	)
	if err != nil {
		t.Fatalf("Compile b = %v", err)
	}
	if a.Provenance.PlanHash != b.Provenance.PlanHash {
		t.Fatalf("PlanHash differs: %q vs %q", a.Provenance.PlanHash, b.Provenance.PlanHash)
	}
	if !a.Provenance.CompiledAt.Equal(b.Provenance.CompiledAt) {
		t.Fatalf("CompiledAt differs: %v vs %v", a.Provenance.CompiledAt, b.Provenance.CompiledAt)
	}
}

// TestCompileValidationWraps confirms that LaunchPlan.Validate errors
// surface wrapped (errors.Is matches the sentinel through the wrap).
func TestCompileValidationWraps(t *testing.T) {
	plan := validPlanForCompile()
	plan.Project.ID = "" // triggers ErrMissingProjectID

	_, err := Compile(context.Background(), plan,
		WithNow(func() time.Time { return fixedTime }),
	)
	if err == nil {
		t.Fatalf("Compile = nil, want error")
	}
	if !errors.Is(err, agentlaunch.ErrMissingProjectID) {
		t.Fatalf("Compile err = %v, want errors.Is %v", err, agentlaunch.ErrMissingProjectID)
	}
}

// TestCompileMatrixErrorWraps confirms that matrix.Lookup failures
// surface through Compile with the sentinel preserved.
func TestCompileMatrixErrorWraps(t *testing.T) {
	plan := validPlanForCompile()
	plan.Provider.ID = "claude"
	plan.Runtime = agentlaunch.RuntimeJsonRpcStdio // not a legal pair for claude

	_, err := Compile(context.Background(), plan,
		WithNow(func() time.Time { return fixedTime }),
	)
	if err == nil {
		t.Fatalf("Compile = nil, want error")
	}
	if !errors.Is(err, matrix.ErrUnsupportedCombo) {
		t.Fatalf("Compile err = %v, want errors.Is matrix.ErrUnsupportedCombo", err)
	}
}

// TestCompileDefaultNowFallback exercises the unspecified-Now path —
// CompiledAt should be roughly the current wall-clock UTC, certainly
// not the zero value.
func TestCompileDefaultNowFallback(t *testing.T) {
	plan := validPlanForCompile()
	before := time.Now().UTC().Add(-time.Second)
	got, err := Compile(context.Background(), plan)
	if err != nil {
		t.Fatalf("Compile = %v", err)
	}
	after := time.Now().UTC().Add(time.Second)
	if got.Provenance.CompiledAt.IsZero() {
		t.Fatalf("CompiledAt is zero with default Now")
	}
	if got.Provenance.CompiledAt.Before(before) || got.Provenance.CompiledAt.After(after) {
		t.Fatalf("CompiledAt = %v, expected between %v and %v", got.Provenance.CompiledAt, before, after)
	}
}

// TestCompileResolvedProviderBinaryOverride confirms that an explicit
// Binary on the plan wins over the matrix default.
func TestCompileResolvedProviderBinaryOverride(t *testing.T) {
	plan := validPlanForCompile()
	plan.Provider.Binary = "/abs/custom/claude"
	got, err := Compile(context.Background(), plan,
		WithNow(func() time.Time { return fixedTime }),
	)
	if err != nil {
		t.Fatalf("Compile = %v", err)
	}
	if got.ResolvedProviderBinary != "/abs/custom/claude" {
		t.Fatalf("ResolvedProviderBinary = %q, want %q", got.ResolvedProviderBinary, "/abs/custom/claude")
	}
}
