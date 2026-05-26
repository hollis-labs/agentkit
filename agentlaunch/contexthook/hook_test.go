package contexthook_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/agentkit/agentcontext"
	"github.com/hollis-labs/agentkit/agentcontext/resolvers"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/contexthook"
)

// newCompiled builds a minimal CompiledLaunch suitable for driving the
// hook in isolation. Use t.TempDir() for ResolvedProjectRoot so resolver
// CWD probes have a real directory to land in.
func newCompiled(t *testing.T) *agentlaunch.CompiledLaunch {
	t.Helper()
	root := t.TempDir()
	plan := agentlaunch.LaunchPlan{
		Project:  agentlaunch.ProjectSpec{ID: "proj", Name: "Project", Root: root},
		Agent:    agentlaunch.AgentSpec{ID: "agent", Name: "Agent"},
		Provider: agentlaunch.ProviderSpec{ID: "claude"},
		Runtime:  agentlaunch.RuntimePTY,
		Workspace: agentlaunch.WorkspaceSpec{
			Mode:         agentlaunch.WorkspaceShared,
			WorkspaceDir: root,
		},
		BootProfile: agentlaunch.BootProfileRef{
			Inline: &agentlaunch.BootProfileInline{
				BootMode: agentlaunch.BootModeStdin,
			},
		},
		Mode: agentlaunch.LaunchInteractive,
		Metadata: agentlaunch.Metadata{
			Labels: map[string]string{
				"role":  "backend",
				"squad": "platform",
			},
		},
	}
	return &agentlaunch.CompiledLaunch{
		Plan:                &plan,
		ResolvedProjectRoot: root,
	}
}

// newProvider builds a DefaultProvider with the full eight-resolver set.
func newProvider(t *testing.T) agentcontext.ContextProvider {
	t.Helper()
	res := resolvers.WithSkillIndex(resolvers.Default())
	p, err := agentcontext.NewProvider(res, agentcontext.DefaultRenderer{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return p
}

// TestNew_NoExtractor_NoOp confirms the documented zero-value behaviour:
// no SlotExtractor → empty bootPrompt, no files planted (even when
// PlantArtifacts=true, because there are no slot results to plant).
func TestNew_NoExtractor_NoOp(t *testing.T) {
	hook := contexthook.New(newProvider(t), contexthook.Config{
		PlantArtifacts: true,
	})

	bootDir := t.TempDir()
	out, err := hook(context.Background(), bootDir, newCompiled(t))
	if err != nil {
		t.Fatalf("hook returned error: %v", err)
	}
	if out != "" {
		t.Fatalf("bootPrompt = %q, want empty", out)
	}
	// Nothing should have been planted.
	if _, err := os.Stat(filepath.Join(bootDir, "context")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("context dir was created without slots; stat err=%v", err)
	}
}

// TestNew_InlineSlot_RendersBootPrompt confirms the basic happy path:
// a single inline slot is assembled and the rendered string surfaces as
// the bootPrompt.
func TestNew_InlineSlot_RendersBootPrompt(t *testing.T) {
	hook := contexthook.New(newProvider(t), contexthook.Config{
		SlotExtractor: func(_ *agentlaunch.CompiledLaunch) ([]agentcontext.SlotSpec, error) {
			return []agentcontext.SlotSpec{
				{
					Name:    "hello",
					Section: "## hello",
					Source: agentcontext.SlotSource{
						Kind:   agentcontext.SlotSourceKindInline,
						Inline: agentcontext.InlineSource{Content: "world"},
					},
				},
			}, nil
		},
	})

	out, err := hook(context.Background(), t.TempDir(), newCompiled(t))
	if err != nil {
		t.Fatalf("hook returned error: %v", err)
	}
	if !strings.Contains(out, "## hello") {
		t.Fatalf("bootPrompt missing section header: %q", out)
	}
	if !strings.Contains(out, "world") {
		t.Fatalf("bootPrompt missing slot body: %q", out)
	}
}

// TestNew_PlantArtifacts_WritesPerSlotFiles confirms PlantArtifacts=true
// writes each non-empty slot to <bootDir>/context/<name>.txt.
func TestNew_PlantArtifacts_WritesPerSlotFiles(t *testing.T) {
	hook := contexthook.New(newProvider(t), contexthook.Config{
		PlantArtifacts: true,
		SlotExtractor: func(_ *agentlaunch.CompiledLaunch) ([]agentcontext.SlotSpec, error) {
			return []agentcontext.SlotSpec{
				{
					Name: "alpha",
					Source: agentcontext.SlotSource{
						Kind:   agentcontext.SlotSourceKindInline,
						Inline: agentcontext.InlineSource{Content: "alpha-body"},
					},
				},
				{
					Name: "beta",
					Source: agentcontext.SlotSource{
						Kind:   agentcontext.SlotSourceKindInline,
						Inline: agentcontext.InlineSource{Content: "beta-body"},
					},
				},
			}, nil
		},
	})

	bootDir := t.TempDir()
	if _, err := hook(context.Background(), bootDir, newCompiled(t)); err != nil {
		t.Fatalf("hook returned error: %v", err)
	}

	for name, want := range map[string]string{
		"alpha": "alpha-body",
		"beta":  "beta-body",
	} {
		path := filepath.Join(bootDir, "context", name+".txt")
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", path, err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}
}

// TestNew_PlantArtifacts_SanitisesFilenames confirms slot names with
// non-safe characters land as sanitised filenames (underscore-fill).
func TestNew_PlantArtifacts_SanitisesFilenames(t *testing.T) {
	hook := contexthook.New(newProvider(t), contexthook.Config{
		PlantArtifacts: true,
		SlotExtractor: func(_ *agentlaunch.CompiledLaunch) ([]agentcontext.SlotSpec, error) {
			return []agentcontext.SlotSpec{
				{
					Name: "needs/sanitisation.md",
					Source: agentcontext.SlotSource{
						Kind:   agentcontext.SlotSourceKindInline,
						Inline: agentcontext.InlineSource{Content: "body"},
					},
				},
			}, nil
		},
	})

	bootDir := t.TempDir()
	if _, err := hook(context.Background(), bootDir, newCompiled(t)); err != nil {
		t.Fatalf("hook returned error: %v", err)
	}
	want := filepath.Join(bootDir, "context", "needs_sanitisation_md.txt")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected sanitised file %s, stat err = %v", want, err)
	}
}

// TestNew_ProviderError_Wrapped confirms provider errors surface
// wrapped under the contexthook layer.
func TestNew_ProviderError_Wrapped(t *testing.T) {
	// Provider with no resolvers will fail with ErrUnknownSlotKind on
	// any slot kind.
	emptyProv, err := agentcontext.NewProvider(
		map[agentcontext.SlotSourceKind]agentcontext.Resolver{},
		agentcontext.DefaultRenderer{},
	)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	hook := contexthook.New(emptyProv, contexthook.Config{
		SlotExtractor: func(_ *agentlaunch.CompiledLaunch) ([]agentcontext.SlotSpec, error) {
			return []agentcontext.SlotSpec{
				{
					Name: "x",
					Source: agentcontext.SlotSource{
						Kind:   agentcontext.SlotSourceKindInline,
						Inline: agentcontext.InlineSource{Content: "y"},
					},
				},
			}, nil
		},
	})

	_, hookErr := hook(context.Background(), t.TempDir(), newCompiled(t))
	if hookErr == nil {
		t.Fatalf("expected error from empty resolver map, got nil")
	}
	if !strings.HasPrefix(hookErr.Error(), "agentlaunch/contexthook:") {
		t.Fatalf("expected wrapped error prefix, got %q", hookErr.Error())
	}
	if !errors.Is(hookErr, agentcontext.ErrUnknownSlotKind) {
		t.Fatalf("expected ErrUnknownSlotKind under wrap, got %v", hookErr)
	}
}

// TestNew_NilProvider_Sentinel confirms a nil provider yields the
// documented sentinel on first invocation.
func TestNew_NilProvider_Sentinel(t *testing.T) {
	hook := contexthook.New(nil, contexthook.Config{})
	_, err := hook(context.Background(), t.TempDir(), newCompiled(t))
	if !errors.Is(err, contexthook.ErrProviderNil) {
		t.Fatalf("expected ErrProviderNil, got %v", err)
	}
}

// TestNew_NilCompiled_Sentinel confirms a nil compiled value yields the
// documented sentinel.
func TestNew_NilCompiled_Sentinel(t *testing.T) {
	hook := contexthook.New(newProvider(t), contexthook.Config{})
	_, err := hook(context.Background(), t.TempDir(), nil)
	if !errors.Is(err, contexthook.ErrCompiledNil) {
		t.Fatalf("expected ErrCompiledNil, got %v", err)
	}
}

// TestNew_ExtractorError_Wrapped confirms a SlotExtractor returning an
// error surfaces wrapped under the contexthook layer.
func TestNew_ExtractorError_Wrapped(t *testing.T) {
	wantErr := errors.New("extractor boom")
	hook := contexthook.New(newProvider(t), contexthook.Config{
		SlotExtractor: func(_ *agentlaunch.CompiledLaunch) ([]agentcontext.SlotSpec, error) {
			return nil, wantErr
		},
	})

	_, err := hook(context.Background(), t.TempDir(), newCompiled(t))
	if err == nil {
		t.Fatalf("expected extractor error to surface")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped extractor error, got %v", err)
	}
	if !strings.Contains(err.Error(), "slot extractor") {
		t.Fatalf("expected error mention of slot extractor, got %q", err.Error())
	}
}

// TestNew_SkillIndex_EndToEnd confirms the full eight-resolver set is
// wired correctly: we discover a real skill on disk and the rendered
// output contains the skill's metadata.
func TestNew_SkillIndex_EndToEnd(t *testing.T) {
	skillsRoot := t.TempDir()
	skillFile := filepath.Join(skillsRoot, "demo-skill.md")
	skillBody := `---
slug: demo-skill
description: A test-fixture skill used by contexthook integration tests.
triggers:
  - demo
  - smoke
---
Demo skill body — not surfaced by the index resolver, only metadata.
`
	if err := os.WriteFile(skillFile, []byte(skillBody), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	hook := contexthook.New(newProvider(t), contexthook.Config{
		SlotExtractor: func(_ *agentlaunch.CompiledLaunch) ([]agentcontext.SlotSpec, error) {
			return []agentcontext.SlotSpec{
				{
					Name:    "skills",
					Section: "## skills",
					Source: agentcontext.SlotSource{
						Kind: agentcontext.SlotSourceKindSkillIndex,
						SkillIndex: agentcontext.SkillIndexSource{
							Roots: []string{skillsRoot},
							Limit: 8,
						},
					},
				},
			}, nil
		},
	})

	out, err := hook(context.Background(), t.TempDir(), newCompiled(t))
	if err != nil {
		t.Fatalf("hook returned error: %v", err)
	}
	if !strings.Contains(out, "demo") {
		t.Fatalf("expected primary trigger 'demo' in rendered output: %q", out)
	}
	if !strings.Contains(out, "A test-fixture skill") {
		t.Fatalf("expected skill description in rendered output: %q", out)
	}
	if !strings.Contains(out, "## skills") {
		t.Fatalf("expected section header in rendered output: %q", out)
	}
}

// TestNew_CustomProvenance confirms Config.ProvenanceFor overrides the
// default and threads through to ContextResult.Provenance.
func TestNew_CustomProvenance(t *testing.T) {
	// We can't read ContextResult.Provenance directly from the hook (it
	// only returns the rendered string), but we can verify the
	// provenance flows by writing a custom resolver that echoes
	// env.RequestProvenance into the rendered output.

	echoResolver := agentcontext.ResolverFunc(func(_ context.Context, spec agentcontext.SlotSpec, env agentcontext.ResolverEnv) (agentcontext.SlotResult, error) {
		return agentcontext.SlotResult{
			Content: "lineage=" + env.RequestProvenance.LineageAlias,
		}, nil
	})

	resMap := map[agentcontext.SlotSourceKind]agentcontext.Resolver{
		agentcontext.SlotSourceKindInline: echoResolver,
	}
	prov, err := agentcontext.NewProvider(resMap, agentcontext.DefaultRenderer{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	hook := contexthook.New(prov, contexthook.Config{
		ProvenanceFor: func(_ *agentlaunch.CompiledLaunch) agentcontext.ProvenanceInput {
			return agentcontext.ProvenanceInput{LineageAlias: "custom.lineage"}
		},
		SlotExtractor: func(_ *agentlaunch.CompiledLaunch) ([]agentcontext.SlotSpec, error) {
			return []agentcontext.SlotSpec{
				{
					Name: "echo",
					Source: agentcontext.SlotSource{
						Kind:   agentcontext.SlotSourceKindInline,
						Inline: agentcontext.InlineSource{Content: "ignored"},
					},
				},
			}, nil
		},
	})

	out, err := hook(context.Background(), t.TempDir(), newCompiled(t))
	if err != nil {
		t.Fatalf("hook returned error: %v", err)
	}
	if !strings.Contains(out, "lineage=custom.lineage") {
		t.Fatalf("expected custom provenance to surface, got %q", out)
	}
}

// TestNew_PlantArtifacts_FilenameCollision confirms that two distinct
// slot names which sanitise to the same filename hard-fail with
// ErrArtifactNameCollision rather than silently overwriting the
// earlier artifact.
func TestNew_PlantArtifacts_FilenameCollision(t *testing.T) {
	hook := contexthook.New(newProvider(t), contexthook.Config{
		PlantArtifacts: true,
		SlotExtractor: func(_ *agentlaunch.CompiledLaunch) ([]agentcontext.SlotSpec, error) {
			// "foo-bar" and "foo_bar" both sanitise to "foo_bar".
			return []agentcontext.SlotSpec{
				{
					Name: "foo-bar",
					Source: agentcontext.SlotSource{
						Kind:   agentcontext.SlotSourceKindInline,
						Inline: agentcontext.InlineSource{Content: "first"},
					},
				},
				{
					Name: "foo_bar",
					Source: agentcontext.SlotSource{
						Kind:   agentcontext.SlotSourceKindInline,
						Inline: agentcontext.InlineSource{Content: "second"},
					},
				},
			}, nil
		},
	})

	_, err := hook(context.Background(), t.TempDir(), newCompiled(t))
	if err == nil {
		t.Fatalf("expected collision error, got nil")
	}
	if !errors.Is(err, contexthook.ErrArtifactNameCollision) {
		t.Fatalf("expected ErrArtifactNameCollision, got %v", err)
	}
}

// TestNew_WorkdirPrecedence verifies the hook resolves the context
// request's Workdir using the same precedence launcher.Prepare follows:
// Workspace.Workdir > ResolvedProjectRoot > Workspace.WorkspaceDir.
func TestNew_WorkdirPrecedence(t *testing.T) {
	// Capture the workdir the resolver was invoked with.
	var capturedWorkdir string
	captureResolver := agentcontext.ResolverFunc(func(_ context.Context, _ agentcontext.SlotSpec, env agentcontext.ResolverEnv) (agentcontext.SlotResult, error) {
		capturedWorkdir = env.Workdir
		return agentcontext.SlotResult{Content: "ok"}, nil
	})
	resMap := map[agentcontext.SlotSourceKind]agentcontext.Resolver{
		agentcontext.SlotSourceKindInline: captureResolver,
	}
	prov, err := agentcontext.NewProvider(resMap, agentcontext.DefaultRenderer{})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	hook := contexthook.New(prov, contexthook.Config{
		SlotExtractor: func(_ *agentlaunch.CompiledLaunch) ([]agentcontext.SlotSpec, error) {
			return []agentcontext.SlotSpec{
				{
					Name: "probe",
					Source: agentcontext.SlotSource{
						Kind:   agentcontext.SlotSourceKindInline,
						Inline: agentcontext.InlineSource{Content: "x"},
					},
				},
			}, nil
		},
	})

	tests := []struct {
		name           string
		workspaceCwd   string
		projectRoot    string
		workspaceDir   string
		wantWorkdir    string
		wantWorkdirMsg string
	}{
		{
			name:           "Workspace.Workdir wins over ResolvedProjectRoot and WorkspaceDir",
			workspaceCwd:   "/tmp/cwd-wins",
			projectRoot:    "/tmp/project-root",
			workspaceDir:   "/tmp/workspace",
			wantWorkdir:    "/tmp/cwd-wins",
			wantWorkdirMsg: "explicit Workspace.Workdir must win",
		},
		{
			name:           "ResolvedProjectRoot wins over WorkspaceDir when Workspace.Workdir empty",
			workspaceCwd:   "",
			projectRoot:    "/tmp/project-root",
			workspaceDir:   "/tmp/workspace",
			wantWorkdir:    "/tmp/project-root",
			wantWorkdirMsg: "ResolvedProjectRoot must win when no Workspace.Workdir",
		},
		{
			name:           "WorkspaceDir last-resort when both prior fields empty",
			workspaceCwd:   "",
			projectRoot:    "",
			workspaceDir:   "/tmp/workspace",
			wantWorkdir:    "/tmp/workspace",
			wantWorkdirMsg: "WorkspaceDir must win when prior fields empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			capturedWorkdir = ""
			compiled := newCompiled(t)
			compiled.Plan.Workspace.Workdir = tc.workspaceCwd
			compiled.Plan.Workspace.WorkspaceDir = tc.workspaceDir
			compiled.ResolvedProjectRoot = tc.projectRoot

			if _, err := hook(context.Background(), t.TempDir(), compiled); err != nil {
				t.Fatalf("hook: %v", err)
			}
			if capturedWorkdir != tc.wantWorkdir {
				t.Fatalf("%s: workdir = %q, want %q", tc.wantWorkdirMsg, capturedWorkdir, tc.wantWorkdir)
			}
		})
	}
}
