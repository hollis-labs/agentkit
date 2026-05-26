package agentcontext

import (
	"errors"
	"testing"
)

func TestContextRequestValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		req  ContextRequest
		want error
	}{
		{
			name: "empty request is valid",
			req:  ContextRequest{},
			want: nil,
		},
		{
			name: "single inline slot is valid",
			req: ContextRequest{
				Slots: []SlotSpec{
					{Name: "intro", Source: SlotSource{Kind: SlotSourceKindInline}},
				},
			},
			want: nil,
		},
		{
			name: "slot validation bubbles up (missing name)",
			req: ContextRequest{
				Slots: []SlotSpec{
					{Source: SlotSource{Kind: SlotSourceKindInline}},
				},
			},
			want: ErrMissingSlotName,
		},
		{
			name: "slot validation bubbles up (unsafe path)",
			req: ContextRequest{
				Slots: []SlotSpec{
					{
						Name: "bad",
						Source: SlotSource{
							Kind:       SlotSourceKindStaticFile,
							StaticFile: StaticFileSource{Path: "a/../b"},
						},
					},
				},
			},
			want: ErrUnsafeSlotPath,
		},
		{
			name: "duplicate slot names rejected",
			req: ContextRequest{
				Slots: []SlotSpec{
					{Name: "intro", Source: SlotSource{Kind: SlotSourceKindInline}},
					{Name: "intro", Source: SlotSource{Kind: SlotSourceKindInline}},
				},
			},
			want: ErrDuplicateSlotName,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.req.Validate()
			if !errors.Is(got, tc.want) {
				t.Errorf("Validate() = %v; want errors.Is(%v)", got, tc.want)
			}
		})
	}
}
