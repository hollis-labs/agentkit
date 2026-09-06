package providerplant

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentlaunch/launcher"
	"github.com/hollis-labs/agentkit/artifact"
	"github.com/hollis-labs/agentkit/materialize"
)

func TestPlant_RepeatedCallsStableAndPreserveSpacePaths(t *testing.T) {
	isolateHome(t)
	projectRoot := t.TempDir() + "/Project With Spaces"
	workspaceRoot := t.TempDir() + "/Workspace With Spaces"
	compiled := compiledFor(t, "codex", agentlaunch.RuntimeSubprocess)
	compiled.Plan.Project.Root = projectRoot
	compiled.Plan.Workspace.WorkspaceDir = workspaceRoot
	compiled.Plan.Workspace.Workdir = projectRoot
	compiled.Plan.Injection.Env = map[string]string{"CALLER": "value"}
	compiled.Plan.Injection.Args = []string{"--caller-flag"}
	compiled.Plan.Injection.NativeFiles = []agentlaunch.NativeFile{{Kind: agentlaunch.NativeFileRaw, RelPath: "bin/tool.sh", Content: "#!/bin/sh\n", Mode: 0o755}}

	prepared, err := launcher.Prepare(context.Background(), compiled)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := Plant(context.Background(), prepared); err != nil {
		t.Fatalf("first plant: %v", err)
	}
	firstArgv := slices.Clone(prepared.Argv)
	firstEnv := cloneStringMap(prepared.Env)
	firstCWD := prepared.Workdir
	if !slices.Contains(firstArgv, projectRoot) {
		t.Fatalf("argv = %v, want project root as one argument", firstArgv)
	}
	assertFileMode(t, prepared.PlantedBootDir, "bin/tool.sh", 0o755)

	if err := Plant(context.Background(), prepared); err != nil {
		t.Fatalf("second plant: %v", err)
	}
	if !reflect.DeepEqual(prepared.Argv, firstArgv) {
		t.Fatalf("argv changed after repeated plant:\nfirst=%v\nsecond=%v", firstArgv, prepared.Argv)
	}
	if !reflect.DeepEqual(prepared.Env, firstEnv) {
		t.Fatalf("env changed after repeated plant:\nfirst=%v\nsecond=%v", firstEnv, prepared.Env)
	}
	if prepared.Workdir != firstCWD {
		t.Fatalf("cwd changed after repeated plant: first=%q second=%q", firstCWD, prepared.Workdir)
	}
}

func TestPrepareExecution_CarriesProviderEffectsDiagnosticsAndMaterialization(t *testing.T) {
	isolateHome(t)
	prepared, err := launcher.Prepare(context.Background(), compiledFor(t, "codex", agentlaunch.RuntimeSubprocess))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	execution, err := PrepareExecution(context.Background(), prepared)
	if err != nil {
		t.Fatalf("PrepareExecution: %v", err)
	}
	if execution.Materialization == nil || !execution.Materialization.Report.Complete {
		t.Fatalf("materialization incomplete: %#v", execution.Materialization)
	}
	if execution.Materialization.Report.Operation != materialize.OperationReconcile {
		t.Fatalf("operation = %q, want reconcile", execution.Materialization.Report.Operation)
	}
	var foundCredential bool
	for _, effect := range execution.Effects {
		if effect.ProviderEffect == "codex-auth-json" && effect.Destination == "auth.json" && effect.Redacted {
			foundCredential = true
		}
	}
	if !foundCredential {
		t.Fatalf("effects = %#v, want redacted codex-auth-json destination", execution.Effects)
	}
	if got := execution.Bindings.Env["CODEX_HOME"].Value; got != prepared.PlantedBootDir {
		t.Fatalf("CODEX_HOME = %q, want boot dir %q", got, prepared.PlantedBootDir)
	}
	if execution.Roots.ProjectRoot == "" || execution.Roots.BootRoot == "" || execution.Bindings.CWD == "" {
		t.Fatalf("roots/bindings not carried: roots=%#v bindings=%#v", execution.Roots, execution.Bindings)
	}
}

func TestPrepareExecution_ProviderDiagnosticsSurvive(t *testing.T) {
	tree := minimalArtifactTree("x.txt", "x")
	proj := agentlaunch.ProviderProjection{
		Diagnostics: []agentlaunch.CapabilityDiagnostic{{Code: "unsupported_feature", Severity: "warning", Feature: "hooks", Message: "hooks require explicit preparation"}},
		Effects:     []agentlaunch.RuntimeEffect{{Kind: agentlaunch.RuntimeEffectHostConfig, Name: "trust", ProviderEffect: "claude-workspace-trust", Destination: ".claude.json", Diagnostic: "host mutation"}},
	}
	req := agentlaunch.PrepareRequest{
		Kind:       agentlaunch.PrepareInputArtifacts,
		Artifacts:  &tree,
		Projection: proj,
		Roots:      agentlaunch.ExecutionRoots{BootRoot: t.TempDir(), CWD: t.TempDir()},
	}
	prepared, err := agentlaunch.ResolvePreparation(context.Background(), req)
	if err != nil {
		t.Fatalf("ResolvePreparation: %v", err)
	}
	if !reflect.DeepEqual(prepared.Diagnostics, proj.Diagnostics) || !reflect.DeepEqual(prepared.Effects, proj.Effects) {
		t.Fatalf("projection metadata changed: diagnostics=%#v effects=%#v", prepared.Diagnostics, prepared.Effects)
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func minimalArtifactTree(path, content string) artifact.Tree {
	return artifact.Tree{Entries: []artifact.Entry{{Path: path, Kind: artifact.EntryFile, Bytes: []byte(content)}}}
}

func TestSubstituteArgPatternPreservesQuotedSpacePaths(t *testing.T) {
	boot := "/tmp/Boot With Spaces"
	project := "/tmp/Project With Spaces"
	got := substituteArgPattern(`--cd "{{.ProjectDir}}" --config '{{.BootDir}}/config.toml' --literal escaped\ value`, boot, project)
	want := []string{"--cd", project, "--config", boot + "/config.toml", "--literal", "escaped value"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("substituteArgPattern() = %#v, want %#v", got, want)
	}
}
