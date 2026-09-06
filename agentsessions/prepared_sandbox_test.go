package agentsessions

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/materialize"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-sandbox/sandbox"
)

type preparedShellBootAdapter struct {
	binary string
	spec   provider.BootDirSpec
}

func (a *preparedShellBootAdapter) Name() string                      { return "prepared-shell" }
func (a *preparedShellBootAdapter) BuildArgs(_, _, _ string) []string { return nil }
func (a *preparedShellBootAdapter) Detect() (string, bool)            { return a.binary, a.binary != "" }
func (a *preparedShellBootAdapter) BootDirSpec() provider.BootDirSpec { return a.spec }
func (a *preparedShellBootAdapter) ParseLine(line []byte) ([]llmtypes.StreamEvent, error) {
	s := strings.TrimRight(string(line), "\r\n")
	switch {
	case strings.HasPrefix(s, "delta:"):
		return []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: strings.TrimPrefix(s, "delta:")}}, nil
	case s == "done":
		return []llmtypes.StreamEvent{{Type: llmtypes.EventDone}}, nil
	}
	return nil, nil
}

func TestNormalizeStartOptions_PreparedExecutionStableBindingsAndPolicy(t *testing.T) {
	project := t.TempDir()
	boot := t.TempDir()
	state := t.TempDir()
	prepared := &agentlaunch.PreparedExecution{
		InputKind:       agentlaunch.PrepareInputArtifacts,
		Materialization: &materialize.Handle{TargetRoot: boot},
		Bindings: agentlaunch.ExecutionBindings{
			Argv: []string{"/bin/sh", "--config", filepath.Join(boot, "config with space.json")},
			Env: map[string]agentlaunch.EnvVar{
				"BETA":  {Value: "2"},
				"ALPHA": {Value: "1"},
			},
			CWD: boot,
		},
		Roots: agentlaunch.ExecutionRoots{ProjectRoot: project, BootRoot: boot, StateRoot: state, CWD: boot},
		Access: agentlaunch.AccessRequirements{
			Mode: agentlaunch.AccessRequired,
			Host: agentlaunch.ExecutionHostLocal,
			Filesystem: []agentlaunch.AccessPath{
				{Mode: agentlaunch.AccessRead, Root: agentlaunch.RootProject, Path: "."},
				{Mode: agentlaunch.AccessWrite, Root: agentlaunch.RootState, Path: "."},
			},
			Subprocess: agentlaunch.SubprocessAccess{Allowed: true},
		},
	}
	original := StartOptions{Workdir: "/caller", Env: []string{"CALLER=1"}, ExtraArgs: []string{"--old"}, AutoPlantBootDir: true, PreparedExecution: prepared}

	normalized, err := normalizeStartOptions(original)
	if err != nil {
		t.Fatalf("normalizeStartOptions: %v", err)
	}

	if normalized.Workdir != boot {
		t.Fatalf("Workdir = %q, want prepared cwd %q", normalized.Workdir, boot)
	}
	if want := []string{"ALPHA=1", "BETA=2"}; !reflect.DeepEqual(normalized.Env, want) {
		t.Fatalf("Env = %v, want %v", normalized.Env, want)
	}
	if want := []string{"--config", filepath.Join(boot, "config with space.json")}; !reflect.DeepEqual(normalized.ExtraArgs, want) {
		t.Fatalf("ExtraArgs = %v, want %v", normalized.ExtraArgs, want)
	}
	if normalized.AutoPlantBootDir {
		t.Fatal("AutoPlantBootDir stayed true for already materialized prepared execution")
	}
	if normalized.SandboxPolicy == nil {
		t.Fatal("SandboxPolicy = nil, want policy derived from prepared access")
	}
	if normalized.SandboxPolicy.Mode != sandbox.ConfinementRequired || normalized.SandboxPolicy.Subprocess != sandbox.SubprocessAllow {
		t.Fatalf("SandboxPolicy = %+v", normalized.SandboxPolicy)
	}
	if len(normalized.SandboxPolicy.Runtime) == 0 || normalized.SandboxPolicy.Runtime[0].Path == "" {
		t.Fatalf("SandboxPolicy.Runtime = %+v, want prepared executable recorded", normalized.SandboxPolicy.Runtime)
	}
	if original.Workdir != "/caller" || !original.AutoPlantBootDir || !reflect.DeepEqual(original.ExtraArgs, []string{"--old"}) {
		t.Fatalf("normalizeStartOptions mutated caller options: %+v", original)
	}
}

func TestSandboxPolicyFromPrepared_FailsClosedForNetworkAndRoots(t *testing.T) {
	prepared := minimalPreparedExecution(t)
	prepared.Access = agentlaunch.AccessRequirements{
		Mode: agentlaunch.AccessRequired,
		Host: agentlaunch.ExecutionHostLocal,
		Filesystem: []agentlaunch.AccessPath{{
			Mode: agentlaunch.AccessRead,
			Root: agentlaunch.RootProject,
			Path: ".",
		}},
		Subprocess: agentlaunch.SubprocessAccess{Allowed: true},
	}
	policy, err := sandboxPolicyFromPrepared(prepared, prepared.Bindings.CWD)
	if err != nil {
		t.Fatalf("sandboxPolicyFromPrepared: %v", err)
	}
	if policy.Network.Mode != sandbox.NetworkDeny {
		t.Fatalf("default network mode = %q, want deny", policy.Network.Mode)
	}

	prepared.Access.Network.Hosts = []string{"api.example.com"}
	_, err = sandboxPolicyFromPrepared(prepared, prepared.Bindings.CWD)
	if !errors.Is(err, ErrPreparedAccessUnsupported) {
		t.Fatalf("hosts err = %v, want ErrPreparedAccessUnsupported", err)
	}

	prepared.Access.Network = agentlaunch.NetworkAccess{}
	prepared.Access.Filesystem = []agentlaunch.AccessPath{{Mode: agentlaunch.AccessRead, Root: agentlaunch.RootKind("typo"), Path: "."}}
	_, err = sandboxPolicyFromPrepared(prepared, prepared.Bindings.CWD)
	if !errors.Is(err, ErrPreparedAccessUnsupported) {
		t.Fatalf("unknown root err = %v, want ErrPreparedAccessUnsupported", err)
	}

	prepared.Access.Filesystem = []agentlaunch.AccessPath{{Mode: agentlaunch.AccessRead, Path: "relative"}}
	_, err = sandboxPolicyFromPrepared(prepared, prepared.Bindings.CWD)
	if !errors.Is(err, ErrPreparedAccessUnsupported) {
		t.Fatalf("relative raw path err = %v, want ErrPreparedAccessUnsupported", err)
	}
}

func TestNormalizeStartOptions_PreparedAccessRemoteRequiredRejected(t *testing.T) {
	prepared := minimalPreparedExecution(t)
	prepared.Access = agentlaunch.AccessRequirements{Mode: agentlaunch.AccessRequired, Host: agentlaunch.ExecutionHostRemote}
	_, err := normalizeStartOptions(StartOptions{PreparedExecution: prepared})
	if !errors.Is(err, ErrPreparedRemoteRequired) {
		t.Fatalf("err = %v, want ErrPreparedRemoteRequired", err)
	}
}

func TestNormalizeStartOptions_RejectsAmbiguousSandboxInputs(t *testing.T) {
	disabledPolicy := &sandbox.ResolvedAccessPolicy{ID: "direct", Mode: sandbox.ConfinementDisabled}
	_, err := normalizeStartOptions(StartOptions{Workdir: t.TempDir(), SandboxPolicy: disabledPolicy, Profile: sandbox.Profile{ID: "legacy"}})
	if !errors.Is(err, ErrSandboxPolicyConflict) {
		t.Fatalf("direct policy/profile err = %v, want ErrSandboxPolicyConflict", err)
	}

	prepared := minimalPreparedExecution(t)
	prepared.Access = agentlaunch.AccessRequirements{Mode: agentlaunch.AccessDisabled, Host: agentlaunch.ExecutionHostLocal}
	_, err = normalizeStartOptions(StartOptions{PreparedExecution: prepared, SandboxPolicy: disabledPolicy})
	if !errors.Is(err, ErrSandboxPolicyConflict) {
		t.Fatalf("prepared policy/direct policy err = %v, want ErrSandboxPolicyConflict", err)
	}
}

func TestNormalizeProviderStartOptions_RejectsRequiredOSPolicy(t *testing.T) {
	prepared := minimalPreparedExecution(t)
	prepared.Access = agentlaunch.AccessRequirements{Mode: agentlaunch.AccessRequired, Host: agentlaunch.ExecutionHostLocal, Roots: prepared.Roots, Subprocess: agentlaunch.SubprocessAccess{Allowed: true}}
	_, err := normalizeProviderStartOptions(StartOptions{PreparedExecution: prepared})
	if err == nil || !strings.Contains(err.Error(), "provider runtime cannot enforce") {
		t.Fatalf("err = %v, want provider-runtime enforcement rejection", err)
	}
}

func TestPrepareSandboxForCommand_RequiredUnsupportedPreventsStart(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	cmd := exec.Command("/bin/sh", "-c", "touch \"$1\"", "sh", marker) //nolint:gosec // test-controlled argv
	outcome, cleanup, err := prepareSandboxForCommand(cmd, StartOptions{
		Workdir: t.TempDir(),
		SandboxPolicy: &sandbox.ResolvedAccessPolicy{
			ID:      "required-none",
			Mode:    sandbox.ConfinementRequired,
			Backend: sandbox.BackendNone,
		},
	})
	if cleanup != nil {
		cleanup()
	}
	var sandboxErr *SandboxError
	if !errors.As(err, &sandboxErr) {
		t.Fatalf("err = %T %[1]v, want *SandboxError", err)
	}
	if outcome.State != sandbox.EnforcementUnsupported || outcome.Enforced {
		t.Fatalf("outcome = %+v, want unsupported/not enforced", outcome)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("marker stat = %v, child appears to have started", statErr)
	}
}

func TestAdapterRuntime_PreparedExecutionBindingsAndSandbox(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test requires POSIX sh")
	}
	workspace := t.TempDir()
	expectedCWD, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("eval workspace symlink: %v", err)
	}
	allowed := filepath.Join(workspace, "allowed.txt")
	if err := os.WriteFile(allowed, []byte("allowed"), 0o644); err != nil {
		t.Fatalf("write allowed: %v", err)
	}
	deniedDir := filepath.Join(workspace, "secret")
	if err := os.MkdirAll(deniedDir, 0o755); err != nil {
		t.Fatalf("mkdir denied: %v", err)
	}
	denied := filepath.Join(deniedDir, "token.txt")
	if err := os.WriteFile(denied, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write denied: %v", err)
	}
	marker := filepath.Join(workspace, "marker.txt")
	script := filepath.Join(workspace, "probe.sh")
	body := `#!/bin/sh
set -eu
if [ "$(pwd)" != "$EXPECTED_CWD" ]; then
  printf 'delta:bad-cwd:%s\n' "$(pwd)"
  exit 20
fi
if [ "${PREPARED_VALUE:-}" != "from-prepared" ]; then
  printf 'delta:bad-env\n'
  exit 21
fi
cat "$1" >/dev/null
if cat "$2" >/dev/null 2>&1; then
  printf 'delta:denied-readable\n'
  exit 22
fi
printf 'started' > "$3"
printf 'delta:ok\n'
printf 'done\n'
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	prepared := &agentlaunch.PreparedExecution{
		InputKind: agentlaunch.PrepareInputArtifacts,
		Bindings: agentlaunch.ExecutionBindings{
			Argv: []string{"/bin/sh", script, allowed, denied, marker},
			Env: map[string]agentlaunch.EnvVar{
				"EXPECTED_CWD":   {Value: expectedCWD},
				"PREPARED_VALUE": {Value: "from-prepared"},
			},
			CWD: workspace,
		},
		Roots: agentlaunch.ExecutionRoots{ProjectRoot: workspace, CWD: workspace},
		Access: agentlaunch.AccessRequirements{
			Mode: agentlaunch.AccessRequired,
			Host: agentlaunch.ExecutionHostLocal,
			Filesystem: []agentlaunch.AccessPath{
				{Mode: agentlaunch.AccessWrite, Root: agentlaunch.RootProject, Path: "."},
				{Mode: agentlaunch.AccessDeny, Root: agentlaunch.RootProject, Path: "secret"},
			},
			RuntimeRead: []string{"/bin/sh"},
			Subprocess:  agentlaunch.SubprocessAccess{Allowed: true},
		},
	}

	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "prepared-adapter", Kind: "cli", Adapter: rejectingPlantAdapter(t, "/bin/sh")})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}
	var fanout bytes.Buffer
	sess, err := rt.Start(context.Background(), StartOptions{PreparedExecution: prepared, Fanout: &fanout})
	if err != nil {
		if errors.As(err, new(*SandboxError)) || strings.Contains(err.Error(), "sandbox") || strings.Contains(err.Error(), "unsupported") {
			t.Skipf("resolved sandbox backend unavailable on %s: %v", runtime.GOOS, err)
		}
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()

	if err := sess.SendInput(context.Background(), []byte("ignored")); err != nil {
		if errors.As(err, new(*SandboxError)) || strings.Contains(err.Error(), "sandbox") || strings.Contains(err.Error(), "unsupported") {
			t.Skipf("resolved sandbox backend unavailable on %s: %v", runtime.GOOS, err)
		}
		t.Fatalf("SendInput: %v\nfanout:\n%s", err, fanout.String())
	}
	if got := fanout.String(); !strings.Contains(got, "ok") {
		t.Fatalf("fanout = %q, want ok", got)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker was not written by sandboxed child: %v", err)
	}
}

func TestPTYRuntime_PreparedExecutionBindingsSurviveRestartAndSkipAutoPlant(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test requires POSIX sh")
	}
	workspace := t.TempDir()
	counter := filepath.Join(workspace, "attempts.txt")
	marker := filepath.Join(workspace, "marker.txt")
	script := filepath.Join(workspace, "restart.sh")
	body := `#!/bin/sh
set -eu
n=0
if [ -f "$1" ]; then
  n=$(cat "$1")
fi
n=$((n + 1))
printf '%s' "$n" > "$1"
if [ "${PREPARED_VALUE:-}" != "$3" ]; then
  exit 30
fi
if [ "$(pwd)" != "$EXPECTED_CWD" ]; then
  exit 31
fi
if [ "$n" -lt 2 ]; then
  exit 1
fi
printf 'done' > "$2"
exit 0
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	expectedCWD, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("eval workspace symlink: %v", err)
	}
	prepared := &agentlaunch.PreparedExecution{
		InputKind:       agentlaunch.PrepareInputArtifacts,
		Materialization: &materialize.Handle{TargetRoot: workspace},
		Bindings: agentlaunch.ExecutionBindings{
			Argv: []string{"/bin/sh", script, counter, marker, "expected"},
			Env: map[string]agentlaunch.EnvVar{
				"EXPECTED_CWD":   {Value: expectedCWD},
				"PREPARED_VALUE": {Value: "expected"},
			},
			CWD: workspace,
		},
		Roots:  agentlaunch.ExecutionRoots{ProjectRoot: workspace, CWD: workspace},
		Access: agentlaunch.AccessRequirements{Mode: agentlaunch.AccessDisabled, Host: agentlaunch.ExecutionHostLocal},
	}
	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "prepared-pty-restart",
		Kind:    "cli",
		Adapter: rejectingPlantAdapter(t, "/bin/sh"),
		Caps:    Capabilities{PTY: true, BinaryRequired: true},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:           "/wrong",
		Env:               []string{"PREPARED_VALUE=wrong"},
		ExtraArgs:         []string{"wrong"},
		AutoPlantBootDir:  true,
		PreparedExecution: prepared,
		LogPath:           filepath.Join(workspace, "session.log"),
		Supervisor: &SupervisorOptions{
			RestartOnCrash:    1,
			MaxRestartBackoff: 10 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()

	done := make(chan error, 1)
	go func() { _, err := sess.Wait(); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Wait: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("prepared restart test timed out")
	}
	if got := strings.TrimSpace(string(mustReadFile(t, counter))); got != "2" {
		t.Fatalf("attempt counter = %q, want 2", got)
	}
	if got := string(mustReadFile(t, marker)); got != "done" {
		t.Fatalf("marker = %q, want done", got)
	}
}

func rejectingPlantAdapter(t *testing.T, binary string) *preparedShellBootAdapter {
	t.Helper()
	return &preparedShellBootAdapter{
		binary: binary,
		spec: provider.BootDirSpec{PlantedFiles: []provider.PlantedFile{{
			RelPath: "AGENTS.md",
			Render: func(provider.PlantContext) (string, error) {
				t.Fatalf("AutoPlantBootDir ran even though PreparedExecution already supplied materialization")
				return "", nil
			},
		}}},
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func TestStreamingStdioRuntime_PreparedExecutionBindingsSkipAutoPlant(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test requires POSIX sh")
	}
	workspace := t.TempDir()
	marker := filepath.Join(workspace, "streaming-marker.txt")
	script := writePreparedLongLivedScript(t, workspace)
	prepared := preparedForLongLivedScript(t, workspace, script, marker)

	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "prepared-streaming",
		Kind:    "cli",
		Adapter: rejectingPlantAdapter(t, "/bin/sh"),
		Caps:    Capabilities{StreamingStdio: true, BinaryRequired: true},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:           "/wrong",
		Env:               []string{"PREPARED_VALUE=wrong"},
		ExtraArgs:         []string{"wrong"},
		AutoPlantBootDir:  true,
		PreparedExecution: prepared,
		LogPath:           filepath.Join(workspace, "streaming.log"),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()
	waitForMarker(t, marker, "started")
}

func TestJsonRpcStdioRuntime_PreparedExecutionBindingsSkipAutoPlant(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test requires POSIX sh")
	}
	workspace := t.TempDir()
	marker := filepath.Join(workspace, "jsonrpc-marker.txt")
	script := writePreparedLongLivedScript(t, workspace)
	prepared := preparedForLongLivedScript(t, workspace, script, marker)

	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "prepared-jsonrpc",
		Kind:    "cli",
		Adapter: rejectingPlantAdapter(t, "/bin/sh"),
		Caps:    Capabilities{JsonRpcStdio: true, BinaryRequired: true},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:           "/wrong",
		Env:               []string{"PREPARED_VALUE=wrong"},
		ExtraArgs:         []string{"wrong"},
		AutoPlantBootDir:  true,
		PreparedExecution: prepared,
		LogPath:           filepath.Join(workspace, "jsonrpc.log"),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()
	waitForMarker(t, marker, "started")
}

func TestServeHTTPRuntime_PreparedExecutionBindingsSkipAutoPlant(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test requires POSIX sh")
	}
	workspace := t.TempDir()
	marker := filepath.Join(workspace, "serve-marker.txt")
	var gotDirectory string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true,"version":"test"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			gotDirectory = r.URL.Query().Get("directory")
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"id":"prepared_http"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			w.Header().Set("content-type", "text/event-stream")
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/global/dispose":
			_, _ = w.Write([]byte(`true`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	script := filepath.Join(workspace, "serve.sh")
	body := `#!/bin/sh
set -eu
if [ "${PREPARED_VALUE:-}" != "$2" ]; then
  exit 40
fi
if [ "$(pwd)" != "$EXPECTED_CWD" ]; then
  exit 41
fi
printf 'started' > "$1"
printf 'opencode server listening on %s\n' "$TEST_SERVER_URL"
trap 'exit 0' TERM INT
while true; do sleep 1; done
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	prepared := preparedForLongLivedScript(t, workspace, script, marker)
	prepared.Bindings.Env["TEST_SERVER_URL"] = agentlaunch.EnvVar{Value: server.URL}

	rt, err := NewFromAdapter(AdapterRuntimeConfig{
		ID:      "prepared-serve",
		Kind:    "cli",
		Adapter: rejectingPlantAdapter(t, "/bin/sh"),
		Caps:    Capabilities{ServeHTTP: true, BinaryRequired: true},
	})
	if err != nil {
		t.Fatalf("NewFromAdapter: %v", err)
	}
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:           "/wrong",
		Env:               append(os.Environ(), "PREPARED_VALUE=wrong"),
		ExtraArgs:         []string{"wrong"},
		AutoPlantBootDir:  true,
		PreparedExecution: prepared,
		LogPath:           filepath.Join(workspace, "serve.log"),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()
	waitForMarker(t, marker, "started")
	if gotDirectory != workspace {
		t.Fatalf("session directory = %q, want %q", gotDirectory, workspace)
	}
}

func writePreparedLongLivedScript(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "long-lived.sh")
	body := `#!/bin/sh
set -eu
if [ "${PREPARED_VALUE:-}" != "$2" ]; then
  exit 40
fi
if [ "$(pwd)" != "$EXPECTED_CWD" ]; then
  exit 41
fi
printf 'started' > "$1"
trap 'exit 0' TERM INT
while true; do sleep 1; done
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func preparedForLongLivedScript(t *testing.T, workspace, script, marker string) *agentlaunch.PreparedExecution {
	t.Helper()
	expectedCWD, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("eval workspace symlink: %v", err)
	}
	return &agentlaunch.PreparedExecution{
		InputKind:       agentlaunch.PrepareInputArtifacts,
		Materialization: &materialize.Handle{TargetRoot: workspace},
		Bindings: agentlaunch.ExecutionBindings{
			Argv: []string{"/bin/sh", script, marker, "expected"},
			Env: map[string]agentlaunch.EnvVar{
				"EXPECTED_CWD":   {Value: expectedCWD},
				"PREPARED_VALUE": {Value: "expected"},
			},
			CWD: workspace,
		},
		Roots:  agentlaunch.ExecutionRoots{ProjectRoot: workspace, CWD: workspace},
		Access: agentlaunch.AccessRequirements{Mode: agentlaunch.AccessDisabled, Host: agentlaunch.ExecutionHostLocal},
	}
}

func waitForMarker(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil && string(b) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	b, _ := os.ReadFile(path)
	t.Fatalf("marker %s = %q, want %q", path, string(b), want)
}

func minimalPreparedExecution(t *testing.T) *agentlaunch.PreparedExecution {
	t.Helper()
	cwd := t.TempDir()
	return &agentlaunch.PreparedExecution{
		InputKind: agentlaunch.PrepareInputArtifacts,
		Bindings:  agentlaunch.ExecutionBindings{Argv: []string{"agent-bin"}, CWD: cwd},
		Roots:     agentlaunch.ExecutionRoots{ProjectRoot: cwd, CWD: cwd},
	}
}
