package agentlaunch

import (
	"errors"
	"testing"
)

func TestCompiledLaunchValidateHappyPath(t *testing.T) {
	p := validPlan()
	c := CompiledLaunch{
		Plan: &p,
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("CompiledLaunch.Validate() = %v, want nil", err)
	}
}

func TestCompiledLaunchValidateErrors(t *testing.T) {
	cases := []struct {
		name    string
		build   func() CompiledLaunch
		wantErr error
	}{
		{
			name: "nil plan",
			build: func() CompiledLaunch {
				return CompiledLaunch{}
			},
			wantErr: ErrCompiledMissingPlan,
		},
		{
			name: "embedded plan fails its own validation",
			build: func() CompiledLaunch {
				p := validPlan()
				p.Project.ID = ""
				return CompiledLaunch{Plan: &p}
			},
			wantErr: ErrMissingProjectID,
		},
		{
			name: "embedded plan has unknown runtime",
			build: func() CompiledLaunch {
				p := validPlan()
				p.Runtime = "unknown"
				return CompiledLaunch{Plan: &p}
			},
			wantErr: ErrUnknownRuntime,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := tc.build().Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want %v", tc.wantErr)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate() = %v, want errors.Is %v", err, tc.wantErr)
			}
		})
	}
}
