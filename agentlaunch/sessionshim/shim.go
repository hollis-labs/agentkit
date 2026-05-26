// Package sessionshim is the go-agent-sessions integration layer for
// go-agent-launch: it converts a fully materialized PreparedLaunch into
// the agentsessions.StartOptions (plus binary path) that
// go-agent-sessions' Manager.Start consumes.
//
// It is a separate subpackage — like contexthook and providerplant —
// so that importing the core agentlaunch package never drags in
// go-agent-sessions. Consumers that hand sessions off to the runtime
// import sessionshim explicitly.
//
// # Why a shim at all
//
// PreparedLaunch models the launch-side fields natively (Env as a map,
// Argv including argv[0]) so the core package stays import-free. The
// runtime's StartOptions uses different shapes (Env as a "K=V" slice,
// the binary split out from Args). This package owns that boundary
// translation so Tether / Torque / Nanite do not each hand-roll it.
//
// # AutoPlantBootDir
//
// A PreparedLaunch produced by providerplant.PrepareAndPlant already has
// its bootdir planted. ToSessionLaunch therefore leaves
// StartOptions.AutoPlantBootDir false: the go-agent-sessions planter
// must not run a second time over the same directory.
package sessionshim

import (
	"errors"
	"fmt"
	"sort"

	"github.com/hollis-labs/agentkit/agentsessions"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/providerplant"
)

// ErrNilPrepared is returned by ToSessionLaunch when the *PreparedLaunch
// argument is nil.
var ErrNilPrepared = errors.New("agentlaunch/sessionshim: nil prepared launch")

// SessionLaunch is the runtime-ready result of converting a
// PreparedLaunch. It pairs the spawn binary (split out of Argv[0]) with
// the StartOptions the go-agent-sessions runtime consumes.
//
// The binary is surfaced separately because go-agent-sessions resolves
// the executable through its adapter + CLI-path wiring, not through
// StartOptions; the caller passes Binary to the bridge constructor
// (e.g. provider.NewPTYBridgeWithAdapter(adapter, Binary)).
type SessionLaunch struct {
	// Binary is the absolute or PATH-resolvable executable to spawn —
	// PreparedLaunch.Argv[0].
	Binary string

	// Options is the StartOptions for agentsessions Manager.Start.
	// StartOptions.ExtraArgs carries PreparedLaunch.Argv[1:].
	Options agentsessions.StartOptions
}

// ToSessionLaunch converts a fully materialized PreparedLaunch into a
// SessionLaunch.
//
// Field mapping:
//
//   - Binary              ← Argv[0]
//   - Options.ExtraArgs   ← Argv[1:]
//   - Options.Workdir     ← Workdir
//   - Options.WorkspaceDir← WorkspaceDir
//   - Options.Env         ← Env, flattened to sorted "K=V" entries
//   - Options.BootPrompt  ← BootPrompt
//   - Options.BootContent ← BootContent
//   - Options.BootMode    ← BootMode
//   - Options.PlantContext← providerplant.PlantContextFor(prepared)
//   - Options.AutoPlantBootDir stays false (bootdir already planted)
//
// The prepared launch must pass PreparedLaunch.Validate (non-nil
// compiled plan, planted bootdir, workspace dir, non-empty argv).
func ToSessionLaunch(prepared *agentlaunch.PreparedLaunch) (SessionLaunch, error) {
	if prepared == nil {
		return SessionLaunch{}, ErrNilPrepared
	}
	if err := prepared.Validate(); err != nil {
		return SessionLaunch{}, fmt.Errorf("agentlaunch/sessionshim: %w", err)
	}

	extraArgs := append([]string(nil), prepared.Argv[1:]...)

	return SessionLaunch{
		Binary: prepared.Argv[0],
		Options: agentsessions.StartOptions{
			Workdir:      prepared.Workdir,
			WorkspaceDir: prepared.WorkspaceDir,
			Env:          envKV(prepared.Env),
			ExtraArgs:    extraArgs,
			BootPrompt:   prepared.BootPrompt,
			BootContent:  prepared.BootContent,
			BootMode:     prepared.BootMode,
			PlantContext: providerplant.PlantContextFor(prepared),
			// AutoPlantBootDir intentionally false — providerplant.Plant
			// already materialized the bootdir.
		},
	}, nil
}

// envKV flattens a map into the sorted "KEY=VALUE" slice form
// StartOptions.Env expects. Nil/empty in → nil out.
func envKV(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}
