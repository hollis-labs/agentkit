package providerplant

import (
	"sort"

	"github.com/hollis-labs/go-providers/provider"

	"github.com/hollis-labs/agentkit/agentlaunch"
)

// PlantContextFor translates a PreparedLaunch into the
// provider.PlantContext the go-providers BootDirSpec renderers consume.
//
// Field mapping:
//
//   - SystemPrompt   ← PreparedLaunch.BootPrompt
//   - BootContent    ← PreparedLaunch.BootContent, falling back to
//     BootPrompt when empty (mirrors go-agent-sessions' back-compat rule
//     so a caller that sets only BootPrompt still gets a populated
//     boot.md)
//   - AgentName      ← PreparedPlantContext.AgentName
//   - MCPLoopbackURL ← PreparedPlantContext.MCPLoopbackURL
//   - ProjectDir     ← PreparedLaunch.Workdir (the project root Prepare
//     resolved; Plant captures it before rewiring Workdir to the spawn
//     cwd)
//   - BootDir        ← PreparedLaunch.PlantedBootDir
//   - MuxCommand / MuxArgs ← PreparedPlantContext.SelfMCPCommand /
//     SelfMCPArgs
//   - MuxEnv              ← PreparedPlantContext.SelfMCPEnv, flattened
//     from the map form to the "KEY=VALUE" slice form
//     provider.PlantContext uses, sorted by key for deterministic
//     planted output
//
// PlantContextFor is exported so the sessionshim package can build the
// same context for StartOptions.PlantContext without duplicating the
// mapping.
func PlantContextFor(prepared *agentlaunch.PreparedLaunch) provider.PlantContext {
	if prepared == nil {
		return provider.PlantContext{}
	}
	pc := provider.PlantContext{
		SystemPrompt:   prepared.BootPrompt,
		BootContent:    prepared.BootContent,
		AgentName:      prepared.PlantContext.AgentName,
		MCPLoopbackURL: prepared.PlantContext.MCPLoopbackURL,
		ProjectDir:     prepared.Workdir,
		BootDir:        prepared.PlantedBootDir,
		MuxCommand:     prepared.PlantContext.SelfMCPCommand,
		MuxArgs:        prepared.PlantContext.SelfMCPArgs,
		MuxEnv:         muxEnvKV(prepared.PlantContext.SelfMCPEnv),
	}
	if pc.BootContent == "" {
		pc.BootContent = pc.SystemPrompt
	}
	return pc
}

// muxEnvKV flattens a map[string]string into the sorted "KEY=VALUE"
// slice form provider.PlantContext.MuxEnv expects. Nil/empty in → nil
// out so the planted MCP entry stays minimal in the common case.
func muxEnvKV(env map[string]string) []string {
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
