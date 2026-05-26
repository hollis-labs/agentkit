package agentlaunch

import (
	"errors"
	"testing"
)

// validPlan returns a LaunchPlan that passes Validate, used as the
// base value the table tests mutate.
func validPlan() LaunchPlan {
	return LaunchPlan{
		Project: ProjectSpec{ID: "proj"},
		Agent:   AgentSpec{ID: "agent"},
		Provider: ProviderSpec{
			ID: "claude",
		},
		Runtime: RuntimePTY,
		Workspace: WorkspaceSpec{
			Mode: WorkspaceShared,
		},
		BootProfile: BootProfileRef{
			CatalogPath: "/abs/path/to/catalog.yaml",
			Name:        "default",
		},
		Mode: LaunchInteractive,
	}
}

func TestLaunchPlanValidateHappyPath(t *testing.T) {
	p := validPlan()
	if err := p.Validate(); err != nil {
		t.Fatalf("validPlan().Validate() = %v, want nil", err)
	}
}

func TestLaunchPlanValidateErrors(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(p *LaunchPlan)
		wantErr error
	}{
		{
			name:    "missing project id",
			mutate:  func(p *LaunchPlan) { p.Project.ID = "" },
			wantErr: ErrMissingProjectID,
		},
		{
			name:    "missing agent id",
			mutate:  func(p *LaunchPlan) { p.Agent.ID = "" },
			wantErr: ErrMissingAgentID,
		},
		{
			name:    "missing provider id",
			mutate:  func(p *LaunchPlan) { p.Provider.ID = "" },
			wantErr: ErrMissingProviderID,
		},
		{
			name:    "unknown runtime",
			mutate:  func(p *LaunchPlan) { p.Runtime = RuntimeKind("does-not-exist") },
			wantErr: ErrUnknownRuntime,
		},
		{
			name:    "zero-value runtime",
			mutate:  func(p *LaunchPlan) { p.Runtime = "" },
			wantErr: ErrUnknownRuntime,
		},
		{
			name:    "unsupported workspace mode",
			mutate:  func(p *LaunchPlan) { p.Workspace.Mode = WorkspaceMode("scratch") },
			wantErr: ErrUnsupportedWorkspaceMode,
		},
		{
			name:    "zero-value workspace mode",
			mutate:  func(p *LaunchPlan) { p.Workspace.Mode = "" },
			wantErr: ErrUnsupportedWorkspaceMode,
		},
		{
			name:    "unsupported launch mode",
			mutate:  func(p *LaunchPlan) { p.Mode = LaunchMode("daemon") },
			wantErr: ErrUnsupportedLaunchMode,
		},
		{
			name:    "zero-value launch mode",
			mutate:  func(p *LaunchPlan) { p.Mode = "" },
			wantErr: ErrUnsupportedLaunchMode,
		},
		{
			name: "boot profile missing entirely",
			mutate: func(p *LaunchPlan) {
				p.BootProfile = BootProfileRef{}
			},
			wantErr: ErrMissingBootProfile,
		},
		{
			name: "inline boot mode invalid",
			mutate: func(p *LaunchPlan) {
				p.BootProfile = BootProfileRef{
					Inline: &BootProfileInline{
						BootPrompt: "hello",
						BootMode:   "exotic",
					},
				}
			},
			wantErr: ErrUnsupportedBootMode,
		},
		{
			name: "inline boot mode empty rejected",
			mutate: func(p *LaunchPlan) {
				p.BootProfile = BootProfileRef{
					Inline: &BootProfileInline{
						BootPrompt: "hello",
					},
				}
			},
			wantErr: ErrUnsupportedBootMode,
		},
		{
			name: "overlay absolute path",
			mutate: func(p *LaunchPlan) {
				p.Injection.BootDirOverlay = map[string]string{
					"/etc/shadow": "nope",
				}
			},
			wantErr: ErrUnsafeInjectionTarget,
		},
		{
			name: "overlay dotdot",
			mutate: func(p *LaunchPlan) {
				p.Injection.BootDirOverlay = map[string]string{
					"../escape.md": "nope",
				}
			},
			wantErr: ErrUnsafeInjectionTarget,
		},
		{
			name: "overlay reserved .git/",
			mutate: func(p *LaunchPlan) {
				p.Injection.BootDirOverlay = map[string]string{
					".git/config": "nope",
				}
			},
			wantErr: ErrUnsafeInjectionTarget,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			p := validPlan()
			tc.mutate(&p)
			err := p.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want %v", tc.wantErr)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate() = %v, want errors.Is %v", err, tc.wantErr)
			}
		})
	}
}

// TestLaunchPlanValidateInlineBootProfileHappy confirms that the three
// recognised inline BootMode tokens all pass validation when the rest
// of the plan is well-formed and CatalogPath is omitted.
func TestLaunchPlanValidateInlineBootProfileHappy(t *testing.T) {
	modes := []string{BootModeNone, BootModeStdin, BootModePlanted}
	for _, mode := range modes {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			p := validPlan()
			p.BootProfile = BootProfileRef{
				Inline: &BootProfileInline{
					BootPrompt: "persona",
					BootMode:   mode,
				},
			}
			if err := p.Validate(); err != nil {
				t.Fatalf("Validate() with inline mode %q = %v, want nil", mode, err)
			}
		})
	}
}

// TestLaunchPlanValidateOverlayAccepts confirms the overlay map accepts
// safe keys when everything else is valid.
func TestLaunchPlanValidateOverlayAccepts(t *testing.T) {
	p := validPlan()
	p.Injection.BootDirOverlay = map[string]string{
		"CLAUDE.md":             "persona",
		"agents/architect.md":   "scoped",
		"scratch/test-data.txt": "ok",
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate() with safe overlay = %v, want nil", err)
	}
}
