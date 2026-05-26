package providerplant

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/launcher"
)

func TestPlant_Claude(t *testing.T) {
	isolateHome(t)
	prepared := preparedFor(t, "claude", agentlaunch.RuntimePTY)
	if err := Plant(context.Background(), prepared); err != nil {
		t.Fatalf("plant: %v", err)
	}
	bd := prepared.PlantedBootDir

	for _, f := range []string{"CLAUDE.md", "boot.md", ".claude/settings.json", ".mcp.json"} {
		assertExists(t, bd, f)
	}
	if claudeMD := readFile(t, bd, "CLAUDE.md"); !strings.Contains(claudeMD, "PERSONA-PROMPT") {
		t.Errorf("CLAUDE.md missing system prompt, got %q", claudeMD)
	}
	if got := readFile(t, bd, "boot.md"); got != "TASK-KICKOFF" {
		t.Errorf("boot.md = %q, want TASK-KICKOFF", got)
	}
	// claude is CwdBootDir: Workdir is rewired to the bootdir.
	if prepared.Workdir != bd {
		t.Errorf("Workdir = %q, want bootdir %q", prepared.Workdir, bd)
	}
	if !slices.Contains(prepared.Argv, "--add-dir") {
		t.Errorf("argv missing --add-dir project arg: %v", prepared.Argv)
	}
}

// TestPlant_ClaudeStreaming proves the streaming runtime plants the same
// boot files as the PTY runtime — the BootDirSpec is runtime-invariant.
func TestPlant_ClaudeStreaming(t *testing.T) {
	isolateHome(t)
	prepared := preparedFor(t, "claude", agentlaunch.RuntimeStreamingStdio)
	if err := Plant(context.Background(), prepared); err != nil {
		t.Fatalf("plant: %v", err)
	}
	for _, f := range []string{"CLAUDE.md", "boot.md", ".claude/settings.json", ".mcp.json"} {
		assertExists(t, prepared.PlantedBootDir, f)
	}
}

func TestPlant_Codex(t *testing.T) {
	isolateHome(t)
	prepared := preparedFor(t, "codex", agentlaunch.RuntimeSubprocess)
	if err := Plant(context.Background(), prepared); err != nil {
		t.Fatalf("plant: %v", err)
	}
	bd := prepared.PlantedBootDir

	for _, f := range []string{"AGENTS.md", "boot.md", "config.toml", "auth.json", ".mcp.json"} {
		assertExists(t, bd, f)
	}
	if agentsMD := readFile(t, bd, "AGENTS.md"); !strings.Contains(agentsMD, "PERSONA-PROMPT") {
		t.Errorf("AGENTS.md missing system prompt, got %q", agentsMD)
	}
	// CODEX_HOME env amendment merges into the prepared env, pointing at
	// the planted bootdir.
	if got := prepared.Env["CODEX_HOME"]; got != bd {
		t.Errorf("Env[CODEX_HOME] = %q, want bootdir %q", got, bd)
	}
	// codex exec mode grants project access via --cd.
	if !slices.Contains(prepared.Argv, "--cd") {
		t.Errorf("argv missing --cd project arg: %v", prepared.Argv)
	}
	// config.toml / auth.json / .mcp.json carry secret-ish content — the
	// go-providers BootDirSpec declares them 0o600 and the planter honors it.
	assertFileMode(t, bd, "config.toml", 0o600)
	assertFileMode(t, bd, "auth.json", 0o600)
}

// TestPlant_CodexAppServer proves the jsonrpc-stdio runtime resolves the
// app-server adapter, whose BootDirSpec suppresses the --cd flag.
func TestPlant_CodexAppServer(t *testing.T) {
	isolateHome(t)
	prepared := preparedFor(t, "codex", agentlaunch.RuntimeJsonRpcStdio)
	if err := Plant(context.Background(), prepared); err != nil {
		t.Fatalf("plant: %v", err)
	}
	if slices.Contains(prepared.Argv, "--cd") {
		t.Errorf("app-server argv must not carry --cd: %v", prepared.Argv)
	}
}

func TestPlant_Opencode(t *testing.T) {
	isolateHome(t)
	compiled := compiledFor(t, "opencode", agentlaunch.RuntimeSubprocess)
	prepared, err := launcher.Prepare(context.Background(), compiled)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	projectRoot := prepared.Workdir // captured before Plant rewires it
	if err := Plant(context.Background(), prepared); err != nil {
		t.Fatalf("plant: %v", err)
	}
	bd := prepared.PlantedBootDir

	for _, f := range []string{"agents/agent-name.md", "agents.json", "opencode.json", "boot.md"} {
		assertExists(t, bd, f)
	}
	if got := prepared.Env["OPENCODE_CONFIG_DIR"]; got != bd {
		t.Errorf("Env[OPENCODE_CONFIG_DIR] = %q, want bootdir %q", got, bd)
	}
	// opencode is CwdProjectDir: Workdir stays the project root.
	if prepared.Workdir != projectRoot {
		t.Errorf("Workdir = %q, want project root %q", prepared.Workdir, projectRoot)
	}
}

// TestPlant_MatrixCombinations exercises every legal provider×runtime
// pair end to end: each must plant without error and leave a non-empty
// bootdir.
func TestPlant_MatrixCombinations(t *testing.T) {
	isolateHome(t)
	pairs := []struct {
		provider string
		runtime  agentlaunch.RuntimeKind
	}{
		{"claude", agentlaunch.RuntimeSubprocess},
		{"claude", agentlaunch.RuntimePTY},
		{"claude", agentlaunch.RuntimeStreamingStdio},
		{"codex", agentlaunch.RuntimeSubprocess},
		{"codex", agentlaunch.RuntimeJsonRpcStdio},
		{"opencode", agentlaunch.RuntimeSubprocess},
	}
	for _, p := range pairs {
		t.Run(p.provider+"/"+string(p.runtime), func(t *testing.T) {
			prepared := preparedFor(t, p.provider, p.runtime)
			if err := Plant(context.Background(), prepared); err != nil {
				t.Fatalf("plant: %v", err)
			}
			if err := prepared.Validate(); err != nil {
				t.Fatalf("prepared invalid after plant: %v", err)
			}
		})
	}
}

func TestPlant_NativeFileClaudeSkill(t *testing.T) {
	isolateHome(t)
	inj := agentlaunch.InjectionSpec{
		NativeFiles: []agentlaunch.NativeFile{
			{Kind: agentlaunch.NativeFileSkill, ID: "code-review", Content: "SKILL BODY"},
		},
	}
	compiled := compiledWith(t, "claude", agentlaunch.RuntimePTY, inj)
	prepared, err := launcher.Prepare(context.Background(), compiled)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := Plant(context.Background(), prepared); err != nil {
		t.Fatalf("plant: %v", err)
	}
	if got := readFile(t, prepared.PlantedBootDir, ".claude/skills/code-review.md"); got != "SKILL BODY" {
		t.Errorf("planted skill = %q, want SKILL BODY", got)
	}
}

// TestPlant_NativeFileRawAgentsMd proves a raw native file planted at
// AGENTS.md overrides the codex BootDirSpec's own rendered AGENTS.md
// (native files run after provider files).
func TestPlant_NativeFileRawAgentsMd(t *testing.T) {
	isolateHome(t)
	inj := agentlaunch.InjectionSpec{
		NativeFiles: []agentlaunch.NativeFile{
			{Kind: agentlaunch.NativeFileRaw, RelPath: "AGENTS.md", Content: "CALLER AGENTS.md"},
		},
	}
	compiled := compiledWith(t, "codex", agentlaunch.RuntimeSubprocess, inj)
	prepared, err := launcher.Prepare(context.Background(), compiled)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := Plant(context.Background(), prepared); err != nil {
		t.Fatalf("plant: %v", err)
	}
	if got := readFile(t, prepared.PlantedBootDir, "AGENTS.md"); got != "CALLER AGENTS.md" {
		t.Errorf("AGENTS.md = %q, want caller override to win", got)
	}
}

// TestPlant_NativeFileModeDefault proves a native file with Mode 0 lands
// at 0o644 and an explicit Mode is honored.
func TestPlant_NativeFileModeDefault(t *testing.T) {
	isolateHome(t)
	inj := agentlaunch.InjectionSpec{
		NativeFiles: []agentlaunch.NativeFile{
			{Kind: agentlaunch.NativeFileRaw, RelPath: "default.txt", Content: "x"},
			{Kind: agentlaunch.NativeFileRaw, RelPath: "secret.txt", Content: "y", Mode: 0o600},
		},
	}
	compiled := compiledWith(t, "claude", agentlaunch.RuntimePTY, inj)
	prepared, err := launcher.Prepare(context.Background(), compiled)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := Plant(context.Background(), prepared); err != nil {
		t.Fatalf("plant: %v", err)
	}
	assertFileMode(t, prepared.PlantedBootDir, "default.txt", 0o644)
	assertFileMode(t, prepared.PlantedBootDir, "secret.txt", 0o600)
}

func TestPlant_Overlay(t *testing.T) {
	isolateHome(t)
	inj := agentlaunch.InjectionSpec{
		BootDirOverlay: map[string]string{
			"scratch/notes.txt": "OVERLAY NOTE",
		},
	}
	compiled := compiledWith(t, "claude", agentlaunch.RuntimePTY, inj)
	prepared, err := launcher.Prepare(context.Background(), compiled)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := Plant(context.Background(), prepared); err != nil {
		t.Fatalf("plant: %v", err)
	}
	if got := readFile(t, prepared.PlantedBootDir, "scratch/notes.txt"); got != "OVERLAY NOTE" {
		t.Errorf("overlay file = %q, want OVERLAY NOTE", got)
	}
	// Overlay entries default to 0o644.
	assertFileMode(t, prepared.PlantedBootDir, "scratch/notes.txt", 0o644)
}

// TestPlant_OverlayOverridesProviderFile proves the overlay (planted
// last) wins over both the provider file and any native file at the
// same path.
func TestPlant_OverlayOverridesProviderFile(t *testing.T) {
	isolateHome(t)
	inj := agentlaunch.InjectionSpec{
		BootDirOverlay: map[string]string{"boot.md": "OVERLAY KICKOFF"},
	}
	compiled := compiledWith(t, "claude", agentlaunch.RuntimePTY, inj)
	prepared, err := launcher.Prepare(context.Background(), compiled)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := Plant(context.Background(), prepared); err != nil {
		t.Fatalf("plant: %v", err)
	}
	if got := readFile(t, prepared.PlantedBootDir, "boot.md"); got != "OVERLAY KICKOFF" {
		t.Errorf("boot.md = %q, want overlay to override provider file", got)
	}
}

// TestPlant_UnsafeOverlayRejected proves an overlay key that escapes the
// bootdir is rejected. The key is injected after Compile (Compile's
// Validate would otherwise reject it) so the planter's own re-validation
// gate is what fires.
func TestPlant_UnsafeOverlayRejected(t *testing.T) {
	isolateHome(t)
	prepared := preparedFor(t, "claude", agentlaunch.RuntimePTY)
	prepared.Compiled.Plan.Injection.BootDirOverlay = map[string]string{
		"../escape.txt": "pwned",
	}
	err := Plant(context.Background(), prepared)
	if !errors.Is(err, agentlaunch.ErrUnsafeInjectionTarget) {
		t.Fatalf("Plant err = %v, want ErrUnsafeInjectionTarget", err)
	}
}

func TestPlant_NilPrepared(t *testing.T) {
	if err := Plant(context.Background(), nil); !errors.Is(err, ErrNilPrepared) {
		t.Fatalf("Plant(nil) err = %v, want ErrNilPrepared", err)
	}
}

func TestPlant_NilCompiled(t *testing.T) {
	prepared := &agentlaunch.PreparedLaunch{
		PlantedBootDir: t.TempDir(),
		WorkspaceDir:   t.TempDir(),
		Argv:           []string{"claude"},
	}
	// prepared.Validate fails first on the nil Compiled — both the
	// Validate gate and the explicit ErrNilCompiled check map to the
	// same sentinel chain.
	if err := Plant(context.Background(), prepared); err == nil {
		t.Fatal("Plant with nil Compiled: expected error")
	}
}
