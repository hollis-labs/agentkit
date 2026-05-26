package agentlaunch

import (
	"errors"
	"testing"
)

func TestValidateOverlayKey(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		wantErr bool
	}{
		// Happy paths.
		{"plain filename", "CLAUDE.md", false},
		{"nested directory", "agents/architect.md", false},
		{"dot-prefixed but not reserved", ".mcp.json", false},
		{"deep nested", "a/b/c/d.md", false},
		{"single-dot segment is fine", "./boot.md", false},

		// Empty.
		{"empty string", "", true},

		// Absolute paths.
		{"absolute posix", "/etc/passwd", true},
		{"absolute backslash", "\\windows\\system32", true},

		// Parent escape.
		{"contains dotdot at start", "../escape.md", true},
		{"contains dotdot in middle", "a/../b/c.md", true},
		{"contains dotdot at end", "a/b/..", true},
		{"backslash dotdot", "..\\escape.md", true},

		// Reserved prefixes / exacts.
		{"reserved .git/", ".git/config", true},
		{"reserved .git exact", ".git", true},
		{"reserved .ssh/", ".ssh/id_rsa", true},
		{"reserved .ssh exact", ".ssh", true},
		{"reserved .gnupg/", ".gnupg/keys", true},
		{"reserved .aws/", ".aws/credentials", true},
		{"backslash to reserved", ".git\\config", true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := validateOverlayKey(tc.key)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("validateOverlayKey(%q) = nil, want error", tc.key)
				}
				if !errors.Is(err, ErrUnsafeInjectionTarget) {
					t.Fatalf("validateOverlayKey(%q) = %v, want errors.Is ErrUnsafeInjectionTarget", tc.key, err)
				}
			} else {
				if err != nil {
					t.Fatalf("validateOverlayKey(%q) = %v, want nil", tc.key, err)
				}
			}
		})
	}
}
