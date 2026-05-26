package agentlaunch

import (
	"errors"
	"testing"
)

func TestNativeFileValidate(t *testing.T) {
	cases := []struct {
		name string
		file NativeFile
		want error // nil = expect success
	}{
		{"valid skill", NativeFile{Kind: NativeFileSkill, ID: "code-review"}, nil},
		{"valid skill dotted id", NativeFile{Kind: NativeFileSkill, ID: "v1.2_review"}, nil},
		{"valid raw", NativeFile{Kind: NativeFileRaw, RelPath: "docs/extra.md"}, nil},
		{"unknown kind", NativeFile{Kind: "bogus", ID: "x"}, ErrUnknownNativeFileKind},
		{"empty kind", NativeFile{ID: "x"}, ErrUnknownNativeFileKind},
		{"skill missing id", NativeFile{Kind: NativeFileSkill}, ErrNativeFileMissingID},
		{"skill id with separator", NativeFile{Kind: NativeFileSkill, ID: "a/b"}, ErrNativeFileUnsafeID},
		{"skill id traversal", NativeFile{Kind: NativeFileSkill, ID: ".."}, ErrNativeFileUnsafeID},
		{"skill id space", NativeFile{Kind: NativeFileSkill, ID: "bad name"}, ErrNativeFileUnsafeID},
		{"raw missing relpath", NativeFile{Kind: NativeFileRaw}, ErrNativeFileMissingRelPath},
		{"raw traversal relpath", NativeFile{Kind: NativeFileRaw, RelPath: "../escape"}, ErrUnsafeInjectionTarget},
		{"raw absolute relpath", NativeFile{Kind: NativeFileRaw, RelPath: "/etc/passwd"}, ErrUnsafeInjectionTarget},
		{"raw reserved relpath", NativeFile{Kind: NativeFileRaw, RelPath: ".git/config"}, ErrUnsafeInjectionTarget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.file.Validate()
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestValidateBootDirRelPath(t *testing.T) {
	ok := []string{"CLAUDE.md", "a/b/c.md", ".claude/skills/x.md"}
	for _, p := range ok {
		if err := ValidateBootDirRelPath(p); err != nil {
			t.Errorf("ValidateBootDirRelPath(%q) = %v, want nil", p, err)
		}
	}
	bad := []string{"", "/abs", "../escape", "a/../../b", ".git/config", ".ssh/id_rsa"}
	for _, p := range bad {
		if err := ValidateBootDirRelPath(p); !errors.Is(err, ErrUnsafeInjectionTarget) {
			t.Errorf("ValidateBootDirRelPath(%q) = %v, want ErrUnsafeInjectionTarget", p, err)
		}
	}
}

// TestLaunchPlanValidateNativeFiles proves LaunchPlan.Validate rejects a
// plan carrying an invalid NativeFile.
func TestLaunchPlanValidateNativeFiles(t *testing.T) {
	plan := LaunchPlan{
		Project:   ProjectSpec{ID: "p"},
		Agent:     AgentSpec{ID: "a"},
		Provider:  ProviderSpec{ID: "claude"},
		Runtime:   RuntimePTY,
		Workspace: WorkspaceSpec{Mode: WorkspaceTemp},
		BootProfile: BootProfileRef{
			Inline: &BootProfileInline{BootMode: BootModePlanted},
		},
		Mode: LaunchInteractive,
		Injection: InjectionSpec{
			NativeFiles: []NativeFile{{Kind: NativeFileSkill}}, // missing ID
		},
	}
	if err := plan.Validate(); !errors.Is(err, ErrNativeFileMissingID) {
		t.Fatalf("Validate() = %v, want ErrNativeFileMissingID", err)
	}
}
