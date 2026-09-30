package agentsessions

import (
	"bytes"
	"errors"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/hollis-labs/go-sandbox/sandbox"
)

func TestDenyGUILaunch_OffIsANoOp(t *testing.T) {
	in := StartOptions{Workdir: t.TempDir()}
	out, err := applyGUILaunchDeny(in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Profile.ID != "" || out.SandboxPolicy != nil {
		t.Fatalf("knob off must not install a sandbox: %+v", out)
	}
}

func TestDenyGUILaunch_ProviderRuntimeRejects(t *testing.T) {
	_, err := normalizeProviderStartOptions(StartOptions{DenyGUILaunch: true})
	if !errors.Is(err, ErrGUILaunchDenyUnsupported) {
		t.Fatalf("provider runtime: err = %v, want ErrGUILaunchDenyUnsupported", err)
	}
}

func TestDenyGUILaunch_UnsupportedPlatformFailsClosed(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("darwin enforces it")
	}
	_, err := applyGUILaunchDeny(StartOptions{DenyGUILaunch: true})
	if !errors.Is(err, ErrGUILaunchDenyUnsupported) {
		t.Fatalf("non-darwin: err = %v, want ErrGUILaunchDenyUnsupported (never run unconfined)", err)
	}
}

func TestDenyGUILaunch_ComposesRatherThanNests(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("enforcement is macOS-only; non-darwin fails closed (see TestDenyGUILaunch_UnsupportedPlatformFailsClosed)")
	}
	dir := t.TempDir()

	t.Run("no sandbox: minimal default-allow profile carrying only the knob", func(t *testing.T) {
		out, err := applyGUILaunchDeny(StartOptions{Workdir: dir, DenyGUILaunch: true})
		if err != nil {
			t.Fatal(err)
		}
		p := out.Profile
		if p.ID != minimalGUIDenyProfileID || !p.DenyGUILaunch || !p.Net || !p.Subprocess || len(p.FS.Deny) != 0 || out.SandboxPolicy != nil {
			t.Fatalf("minimal profile = %+v / policy %v", p, out.SandboxPolicy)
		}
	})

	t.Run("existing Profile gets the knob on a copy", func(t *testing.T) {
		in := StartOptions{Workdir: dir, DenyGUILaunch: true, Profile: sandbox.Profile{ID: "mine", Net: false, Subprocess: true}}
		out, err := applyGUILaunchDeny(in)
		if err != nil {
			t.Fatal(err)
		}
		if out.Profile.ID != "mine" || !out.Profile.DenyGUILaunch || out.Profile.Net {
			t.Fatalf("profile not merged: %+v", out.Profile)
		}
	})

	t.Run("existing SandboxPolicy is merged, not mutated or replaced", func(t *testing.T) {
		policy, err := sandbox.ResolveAccessPolicy(sandbox.AccessPolicy{
			ID: "mine", Mode: sandbox.ConfinementRequired,
			Roots:   sandbox.Roots{Project: dir, CWD: dir},
			Network: sandbox.NetworkAccess{Mode: sandbox.NetworkFull},
			FS:      sandbox.FilesystemAccess{Read: []sandbox.PathRef{{Path: "/usr/bin"}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		out, err := applyGUILaunchDeny(StartOptions{Workdir: dir, DenyGUILaunch: true, SandboxPolicy: &policy})
		if err != nil {
			t.Fatal(err)
		}
		if out.SandboxPolicy == nil || !out.SandboxPolicy.DenyGUILaunch || out.SandboxPolicy.ID != "mine" || out.Profile.ID != "" {
			t.Fatalf("policy not merged: %+v profile %+v", out.SandboxPolicy, out.Profile)
		}
		if policy.DenyGUILaunch {
			t.Fatal("caller's policy was mutated")
		}
	})

	t.Run("disabled policy becomes the minimal profile", func(t *testing.T) {
		policy, err := sandbox.ResolveAccessPolicy(sandbox.AccessPolicy{ID: "off", Mode: sandbox.ConfinementDisabled, Backend: sandbox.BackendAuto, Roots: sandbox.Roots{Project: dir, CWD: dir}})
		if err != nil {
			t.Fatal(err)
		}
		out, err := applyGUILaunchDeny(StartOptions{Workdir: dir, DenyGUILaunch: true, SandboxPolicy: &policy})
		if err != nil {
			t.Fatal(err)
		}
		if out.SandboxPolicy != nil || out.Profile.ID != minimalGUIDenyProfileID {
			t.Fatalf("disabled policy: %+v profile %+v", out.SandboxPolicy, out.Profile)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		once, err := applyGUILaunchDeny(StartOptions{Workdir: dir, DenyGUILaunch: true})
		if err != nil {
			t.Fatal(err)
		}
		twice, err := applyGUILaunchDeny(once)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(twice.Profile, once.Profile) {
			t.Fatalf("second pass changed the profile: %+v vs %+v", once.Profile, twice.Profile)
		}
	})
}

// A real process, through the real spawn-boundary function, under the real
// sandbox: open(1) must not exec, and an ordinary binary must still run.
func TestDenyGUILaunch_RealSpawnBoundary(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox-exec only")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not found")
	}
	opts, err := normalizeStartOptions(StartOptions{Workdir: t.TempDir(), DenyGUILaunch: true})
	if err != nil {
		t.Fatal(err)
	}
	run := func(bin string, args ...string) (string, int) {
		cmd := exec.Command(bin, args...)
		outcome, cleanup, err := prepareSandboxForCommand(cmd, opts)
		if err != nil {
			t.Fatalf("prepareSandboxForCommand: %+v %v", outcome, err)
		}
		defer cleanup()
		if !outcome.Enforced {
			t.Fatalf("not enforced: %+v", outcome)
		}
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		code := 0
		if err := cmd.Run(); err != nil {
			ee, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("run %s: %v", bin, err)
			}
			code = ee.ExitCode()
		}
		return out.String(), code
	}
	if out, code := run("/usr/bin/open", "-h"); code != 71 || !strings.Contains(out, "Operation not permitted") {
		t.Fatalf("open was not blocked (exit %d): %s", code, out)
	}
	if out, code := run("/usr/bin/true"); code != 0 {
		t.Fatalf("/usr/bin/true blocked (exit %d): %s", code, out)
	}
}
