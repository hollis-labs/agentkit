package agentcontext

import (
	"errors"
	"testing"
)

func TestSlotSourceKindValid(t *testing.T) {
	t.Parallel()
	valid := []SlotSourceKind{
		SlotSourceKindStaticFile,
		SlotSourceKindStaticDir,
		SlotSourceKindInline,
		SlotSourceKindCmd,
		SlotSourceKindHTTPText,
		SlotSourceKindHTTPJSON,
		SlotSourceKindRoleSummary,
		SlotSourceKindSkillIndex,
	}
	for _, k := range valid {
		if !k.Valid() {
			t.Errorf("kind %q should be valid", k)
		}
	}
	invalid := []SlotSourceKind{"", "vanta_recall", "bogus", "STATIC_FILE"}
	for _, k := range invalid {
		if k.Valid() {
			t.Errorf("kind %q should NOT be valid", k)
		}
	}
}

func TestSlotSpecValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		spec SlotSpec
		want error
	}{
		{
			name: "empty name fails",
			spec: SlotSpec{Source: SlotSource{Kind: SlotSourceKindInline}},
			want: ErrMissingSlotName,
		},
		{
			name: "whitespace-only name fails",
			spec: SlotSpec{Name: "   ", Source: SlotSource{Kind: SlotSourceKindInline}},
			want: ErrMissingSlotName,
		},
		{
			name: "empty kind fails",
			spec: SlotSpec{Name: "a"},
			want: ErrUnknownSlotKind,
		},
		{
			name: "unknown kind fails",
			spec: SlotSpec{Name: "a", Source: SlotSource{Kind: "bogus"}},
			want: ErrUnknownSlotKind,
		},
		{
			name: "static_file with parent segment fails",
			spec: SlotSpec{
				Name: "a",
				Source: SlotSource{
					Kind:       SlotSourceKindStaticFile,
					StaticFile: StaticFileSource{Path: "foo/../etc/passwd"},
				},
			},
			want: ErrUnsafeSlotPath,
		},
		{
			name: "static_dir with parent segment fails",
			spec: SlotSpec{
				Name: "a",
				Source: SlotSource{
					Kind:      SlotSourceKindStaticDir,
					StaticDir: StaticDirSource{Path: "../etc"},
				},
			},
			want: ErrUnsafeSlotPath,
		},
		{
			name: "role_summary with parent segment fails",
			spec: SlotSpec{
				Name: "a",
				Source: SlotSource{
					Kind:        SlotSourceKindRoleSummary,
					RoleSummary: RoleSummarySource{Path: "../../escape.md"},
				},
			},
			want: ErrUnsafeSlotPath,
		},
		{
			name: "absolute path is fine",
			spec: SlotSpec{
				Name: "a",
				Source: SlotSource{
					Kind:       SlotSourceKindStaticFile,
					StaticFile: StaticFileSource{Path: "/etc/hosts"},
				},
			},
			want: nil,
		},
		{
			name: "tilde path is fine",
			spec: SlotSpec{
				Name: "a",
				Source: SlotSource{
					Kind:       SlotSourceKindStaticFile,
					StaticFile: StaticFileSource{Path: "~/role.md"},
				},
			},
			want: nil,
		},
		{
			name: "embedded dots are fine (only literal '..' segments are unsafe)",
			spec: SlotSpec{
				Name: "a",
				Source: SlotSource{
					Kind:       SlotSourceKindStaticFile,
					StaticFile: StaticFileSource{Path: "foo..bar/baz"},
				},
			},
			want: nil,
		},
		{
			name: "inline with empty content is fine (caller's choice)",
			spec: SlotSpec{Name: "a", Source: SlotSource{Kind: SlotSourceKindInline}},
			want: nil,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.spec.Validate()
			if !errors.Is(got, tc.want) {
				t.Errorf("Validate() = %v; want errors.Is(%v)", got, tc.want)
			}
		})
	}
}
