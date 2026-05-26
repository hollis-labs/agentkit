package agentlaunch

import (
	"errors"
	"testing"
)

// validPrepared returns a PreparedLaunch that passes Validate.
func validPrepared() PreparedLaunch {
	p := validPlan()
	c := CompiledLaunch{Plan: &p}
	return PreparedLaunch{
		Compiled:       &c,
		PlantedBootDir: "/tmp/bootdir",
		WorkspaceDir:   "/tmp/ws",
		Argv:           []string{"/usr/bin/claude", "--model", "sonnet"},
	}
}

func TestPreparedLaunchValidateHappyPath(t *testing.T) {
	if err := validPrepared().Validate(); err != nil {
		t.Fatalf("validPrepared().Validate() = %v, want nil", err)
	}
}

func TestPreparedLaunchValidateErrors(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(p *PreparedLaunch)
		wantErr error
	}{
		{
			name:    "nil compiled",
			mutate:  func(p *PreparedLaunch) { p.Compiled = nil },
			wantErr: ErrCompiledMissingPlan,
		},
		{
			name: "compiled with nil plan",
			mutate: func(p *PreparedLaunch) {
				p.Compiled = &CompiledLaunch{}
			},
			wantErr: ErrCompiledMissingPlan,
		},
		{
			name: "embedded plan fails validation",
			mutate: func(p *PreparedLaunch) {
				bad := validPlan()
				bad.Agent.ID = ""
				p.Compiled = &CompiledLaunch{Plan: &bad}
			},
			wantErr: ErrMissingAgentID,
		},
		{
			name:    "missing planted bootdir",
			mutate:  func(p *PreparedLaunch) { p.PlantedBootDir = "" },
			wantErr: ErrPreparedMissingBootDir,
		},
		{
			name:    "missing workspace dir",
			mutate:  func(p *PreparedLaunch) { p.WorkspaceDir = "" },
			wantErr: ErrPreparedMissingWorkspaceDir,
		},
		{
			name:    "empty argv",
			mutate:  func(p *PreparedLaunch) { p.Argv = nil },
			wantErr: ErrPreparedMissingArgv,
		},
		{
			name:    "zero-len argv slice",
			mutate:  func(p *PreparedLaunch) { p.Argv = []string{} },
			wantErr: ErrPreparedMissingArgv,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			pl := validPrepared()
			tc.mutate(&pl)
			err := pl.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want %v", tc.wantErr)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate() = %v, want errors.Is %v", err, tc.wantErr)
			}
		})
	}
}
