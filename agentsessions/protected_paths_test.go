package agentsessions

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/go-sandbox/sandbox"
)

// CW-20260930-0237: StartOptions.ProtectedPaths write-protects a host's
// control-plane state from the agent it launches.

func protectSupported(t *testing.T) {
	t.Helper()
	caps := sandbox.ResolveBackendCapabilities("", sandbox.BackendAuto)
	if !caps.Supported || !slices.Contains(caps.Capabilities, sandbox.CapWriteProtect) {
		t.Skipf("the %s backend on %s cannot write-protect (see TestProtectedPaths_UnsupportedPlatformFailsClosed)", caps.Backend, caps.GOOS)
	}
}

func TestProtectedPaths_OffIsANoOp(t *testing.T) {
	out, err := applyProtectedPaths(StartOptions{Workdir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if out.Profile.ID != "" || out.SandboxPolicy != nil {
		t.Fatalf("no protected paths must not install a sandbox: %+v", out)
	}
}

func TestProtectedPaths_RejectsRelativePaths(t *testing.T) {
	if _, err := applyProtectedPaths(StartOptions{ProtectedPaths: []string{"state"}}); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative protected path: err = %v, want a refusal", err)
	}
}

func TestProtectedPaths_ProviderRuntimeRejects(t *testing.T) {
	_, err := normalizeProviderStartOptions(StartOptions{ProtectedPaths: []string{"/var/lib/app"}})
	if !errors.Is(err, ErrProtectedPathsUnsupported) {
		t.Fatalf("provider runtime: err = %v, want ErrProtectedPathsUnsupported", err)
	}
}

func TestProtectedPaths_UnsupportedPlatformFailsClosed(t *testing.T) {
	caps := sandbox.ResolveBackendCapabilities("", sandbox.BackendAuto)
	if caps.Supported && slices.Contains(caps.Capabilities, sandbox.CapWriteProtect) {
		t.Skipf("%s enforces it", runtime.GOOS)
	}
	if _, err := applyProtectedPaths(StartOptions{ProtectedPaths: []string{"/var/lib/app"}}); !errors.Is(err, ErrProtectedPathsUnsupported) {
		t.Fatalf("err = %v, want ErrProtectedPathsUnsupported (never run with the control plane writable)", err)
	}
}

func TestProtectedPaths_ComposesRatherThanNests(t *testing.T) {
	protectSupported(t)
	dir := t.TempDir()
	state := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(state, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("no sandbox: minimal host-filesystem profile carrying only the protection", func(t *testing.T) {
		out, err := applyProtectedPaths(StartOptions{Workdir: dir, ProtectedPaths: []string{state}})
		if err != nil {
			t.Fatal(err)
		}
		p := out.Profile
		if p.ID != minimalProtectProfileID || !p.HostFilesystem || !p.Net || !p.Subprocess || !slices.Equal(p.FS.Protect, []string{state}) || len(p.FS.Deny) != 0 || out.SandboxPolicy != nil {
			t.Fatalf("minimal profile = %+v / policy %v", p, out.SandboxPolicy)
		}
	})

	t.Run("existing Profile gets the paths on a copy", func(t *testing.T) {
		mine := sandbox.Profile{ID: "mine", Subprocess: true, FS: sandbox.FSSpec{Write: []string{"workspace"}, Protect: make([]string, 1, 4)}}
		mine.FS.Protect[0] = "/already"
		out, err := applyProtectedPaths(StartOptions{Workdir: dir, ProtectedPaths: []string{state}, Profile: mine})
		if err != nil {
			t.Fatal(err)
		}
		if out.Profile.ID != "mine" || out.Profile.HostFilesystem || !slices.Equal(out.Profile.FS.Protect, []string{"/already", state}) {
			t.Fatalf("profile not merged: %+v", out.Profile)
		}
		if got := mine.FS.Protect[:cap(mine.FS.Protect)][1]; got != "" {
			t.Fatalf("caller's Protect backing array was written: %q", got)
		}
	})

	t.Run("existing SandboxPolicy is merged, not mutated or replaced", func(t *testing.T) {
		policy, err := sandbox.ResolveAccessPolicy(sandbox.AccessPolicy{
			ID: "mine", Mode: sandbox.ConfinementRequired,
			Roots:   sandbox.Roots{Project: dir, CWD: dir},
			Network: sandbox.NetworkAccess{Mode: sandbox.NetworkFull},
			FS:      sandbox.FilesystemAccess{Write: []sandbox.PathRef{{Root: sandbox.ProjectRoot}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		out, err := applyProtectedPaths(StartOptions{Workdir: dir, ProtectedPaths: []string{state}, SandboxPolicy: &policy})
		if err != nil {
			t.Fatal(err)
		}
		want, _ := filepath.EvalSymlinks(state)
		if out.SandboxPolicy == nil || out.SandboxPolicy.ID != "mine" || len(out.SandboxPolicy.FS.Protect) != 1 || out.SandboxPolicy.FS.Protect[0].Path != want || out.Profile.ID != "" {
			t.Fatalf("policy not merged: %+v profile %+v", out.SandboxPolicy, out.Profile)
		}
		if len(policy.FS.Protect) != 0 {
			t.Fatal("caller's policy was mutated")
		}
	})

	t.Run("disabled policy becomes the minimal profile", func(t *testing.T) {
		policy, err := sandbox.ResolveAccessPolicy(sandbox.AccessPolicy{ID: "off", Mode: sandbox.ConfinementDisabled, Backend: sandbox.BackendAuto, Roots: sandbox.Roots{Project: dir, CWD: dir}})
		if err != nil {
			t.Fatal(err)
		}
		out, err := applyProtectedPaths(StartOptions{Workdir: dir, ProtectedPaths: []string{state}, SandboxPolicy: &policy})
		if err != nil {
			t.Fatal(err)
		}
		if out.SandboxPolicy != nil || out.Profile.ID != minimalProtectProfileID || !out.Profile.HostFilesystem {
			t.Fatalf("disabled policy: %+v profile %+v", out.SandboxPolicy, out.Profile)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		once, err := normalizeStartOptions(StartOptions{Workdir: dir, ProtectedPaths: []string{state}})
		if err != nil {
			t.Fatal(err)
		}
		twice, err := normalizeStartOptions(once)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(twice.Profile, once.Profile) {
			t.Fatalf("second pass changed the profile: %+v vs %+v", once.Profile, twice.Profile)
		}
	})
}

// The behaviour the task asks for, through a real agent turn: an adapter
// runtime launched with a registered control-plane dir and no sandbox of its
// own gets the default protect-only sandbox; its CLI cannot write the
// registered dir and can still write its workdir.
func TestProtectedPaths_AgentTurnCannotWriteRegisteredPath(t *testing.T) {
	protectSupported(t)
	switch runtime.GOOS {
	case "linux":
		if _, err := exec.LookPath("bwrap"); err != nil {
			t.Skip("bwrap not installed")
		}
		if out, err := exec.Command("bwrap", "--dev-bind", "/", "/", "--", "/bin/true").CombinedOutput(); err != nil {
			t.Skipf("bwrap cannot create a mount namespace here: %v: %s", err, out)
		}
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			t.Skip("sandbox-exec not installed")
		}
	}
	work := t.TempDir()
	state := t.TempDir() // the host's control-plane dir, outside the workdir
	allow := filepath.Join(state, "workflow-unattended.json")
	if err := os.WriteFile(allow, []byte(`{"allow":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(work, "fake-agent.sh")
	body := "#!/bin/sh\n" +
		"if printf '{\"allow\":[\"*\"]}' > " + shellQuote(allow) + " 2>/dev/null; then printf 'delta:WROTE-CONTROL-PLANE\\n'; else printf 'delta:control-plane-refused\\n'; fi\n" +
		"if printf 'new' > " + shellQuote(filepath.Join(state, "planted.json")) + " 2>/dev/null; then printf 'delta:CREATED-IN-CONTROL-PLANE\\n'; fi\n" +
		"printf 'ok' > " + shellQuote(filepath.Join(work, "agent-output")) + " && printf 'delta:workdir-ok\\n'\n" +
		"printf 'done\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	rt, err := NewFromAdapter(AdapterRuntimeConfig{ID: "protect-turn", Kind: "cli", Adapter: &echoAdapter{script: script}})
	if err != nil {
		t.Fatal(err)
	}
	var fanout bytes.Buffer
	var outcomes []SandboxOutcome
	sess, err := rt.Start(context.Background(), StartOptions{
		Workdir:                work,
		Fanout:                 &fanout,
		ProtectedPaths:         []string{state},
		SandboxOutcomeCallback: func(o SandboxOutcome) { outcomes = append(outcomes, o) },
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = sess.Stop(context.Background()) }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := sess.SendInput(ctx, []byte("rewrite the allow-list")); err != nil {
		t.Fatalf("SendInput: %v", err)
	}

	got := fanout.String()
	if !strings.Contains(got, "control-plane-refused") || strings.Contains(got, "WROTE") || strings.Contains(got, "CREATED") || !strings.Contains(got, "workdir-ok") {
		t.Fatalf("agent turn output = %q; want the control-plane write refused and the workdir write allowed", got)
	}
	if data, _ := os.ReadFile(allow); string(data) != `{"allow":[]}` {
		t.Fatalf("allow-list = %q after the turn, want unchanged", data)
	}
	if _, err := os.Stat(filepath.Join(state, "planted.json")); err == nil {
		t.Fatal("the agent created a file in the protected dir")
	}
	if len(outcomes) == 0 || !outcomes[len(outcomes)-1].Enforced || outcomes[len(outcomes)-1].PolicyID != minimalProtectProfileID {
		t.Fatalf("sandbox outcomes = %+v, want the enforced %s profile", outcomes, minimalProtectProfileID)
	}
}
