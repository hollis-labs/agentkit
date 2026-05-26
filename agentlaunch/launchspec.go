package agentlaunch

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// S4.4 — Collapse launches/ + boot-profiles/ into Specs + templates.
//
// This file is the parameterized re-expression of the historical
// ~/.tether/catalog/launches/ + ~/.tether/catalog/boot-profiles/ file
// explosion. It BUILDS ON the locked S4.1 AssemblySpec, the S4.2 var
// surface, and the S4.3 materializer. It adds NO new field shapes to
// those frozen types: a LaunchSpec is an AssemblySpec, and a LaunchBag is
// just the inputs/vars bag the AssemblySpec already renders against.
//
// The problem this solves:
//
//   The legacy catalog had 64 launches/ files and ~50 boot-profiles/
//   files. Every (agent × project × provider × workspace-mode) tuple was
//   a frozen file, AND every launch had a `.worktree` TWIN file that
//   differed only in `workspace.mode`. That is a file explosion with two
//   redundant axes baked into identity:
//
//     - workspace MODE was an identity axis (the `.worktree` twins);
//     - PROVIDER was an identity axis (claude / codex / opencode launch
//       files for the same project).
//
//   D3 says the blueprint is parameterized and runtime-binding
//   (provider/runner) is a DISTINCT kind. So neither mode nor provider
//   belongs in blueprint identity:
//
//     - workspace MODE becomes a plain INPUT on the spec — one value,
//       no twin file. This is what "kills the .worktree twins."
//     - PROVIDER becomes a runner INPUT that feeds the runtime-binding,
//       NOT the blueprint identity (D3).
//
//   One LaunchSpec + many LaunchBag input bags replaces the whole grid.
//
// Scope: launches/ + boot-profiles/ ONLY. projects/ and
// sandbox-profiles/ are explicitly out of scope (they have no registry
// kind — a known, deliberately-deferred follow-up).

// LaunchSpec sentinel errors. errors.Is-comparable.
var (
	// ErrLaunchSpecMissingWorkDir is returned by ValidateMinimumConfig
	// when a launch input bag declares no work directory. Work dir is one
	// of the two mandatory minimum-config knobs.
	ErrLaunchSpecMissingWorkDir = errors.New("agentlaunch: launch minimum config requires a work_dir")

	// ErrLaunchSpecMissingRunner is returned by ValidateMinimumConfig when
	// a launch input bag declares no runner. Runner (the provider that
	// feeds the runtime-binding, D3) is the second mandatory knob.
	ErrLaunchSpecMissingRunner = errors.New("agentlaunch: launch minimum config requires a runner")

	// ErrLaunchSpecUnknownInput is returned when a LaunchBag carries an
	// input key the LaunchSpec does not declare. A bag that silently
	// smuggles an unknown key is worse than an error: it would render as
	// nothing and hide a typo.
	ErrLaunchSpecUnknownInput = errors.New("agentlaunch: launch input bag references an undeclared input")
)

// Minimum-config input names. The minimum valid launch config is exactly
// two required knobs — work dir + runner — plus two optional knobs
// (isolation + bus). Nothing else is required: every other historical
// launch/boot-profile field (project, agent, prompt flags, lineage,
// slots) is either derivable or a non-mandatory input with a default.
const (
	// LaunchInputWorkDir is the agent's working directory — the repo or
	// tree the session operates on. REQUIRED. Half of the minimum config.
	LaunchInputWorkDir = "work_dir"

	// LaunchInputRunner is the runner/provider id (claude-code, codex-cli,
	// opencode, ...). REQUIRED. The other half of the minimum config.
	// Per D3 this feeds the runtime-binding, never the blueprint identity.
	LaunchInputRunner = "runner"

	// LaunchInputIsolation is the workspace-isolation mode. OPTIONAL — the
	// only other knob besides bus. This is the input that replaces the
	// `.worktree` twin files: mode is one value, not a second file.
	// Values mirror the legacy Tether tokens ("hybrid", "worktree").
	LaunchInputIsolation = "isolation"

	// LaunchInputBus is the message-bus topic / attach posture. OPTIONAL.
	// Empty means the runner's default bus posture.
	LaunchInputBus = "bus"
)

// minimumConfigInputs is the canonical declared-input set for the
// minimum-valid LaunchSpec: two required, two optional. A LaunchSpec
// that omits any of these is not a valid minimum spec.
var minimumConfigInputs = []string{
	LaunchInputWorkDir,
	LaunchInputRunner,
	LaunchInputIsolation,
	LaunchInputBus,
}

// LaunchSpec is the parameterized re-expression of a historical Tether
// launch (plus its folded boot-profile). It IS an AssemblySpec — the
// embedded type carries the locked S4.1 contract — with a thin S4.4
// wrapper that pins the minimum-config input contract and a stable
// catalog id.
//
// One LaunchSpec replaces a whole row of the legacy grid: instead of
// nanite-claude.yaml + nanite-claude-worktree.yaml + nanite-codex-* +
// ... , there is ONE LaunchSpec and one LaunchBag per concrete launch.
type LaunchSpec struct {
	// AssemblySpec is the embedded S4.1 contract: typed Inputs, Vars,
	// Files, Injections, Runtime, plus the merge-tag Template body.
	AssemblySpec `yaml:",inline" json:",inline"`

	// ID is the stable catalog identifier for this launch family (for
	// example "tether.launch"). It is the registry name a BootSpec /
	// execution-template record is published under.
	ID string `yaml:"id" json:"id"`

	// DisplayName is the human-readable label.
	DisplayName string `yaml:"display_name,omitempty" json:"display_name,omitempty"`
}

// Validate enforces the embedded AssemblySpec contract plus the S4.4
// minimum-config contract: the spec MUST declare the two required
// minimum-config inputs (work_dir, runner) and SHOULD declare the two
// optional ones (isolation, bus). A spec missing a required input could
// never be rendered into a valid launch.
func (s LaunchSpec) Validate() error {
	if s.ID == "" {
		return ErrRegistryMissingName
	}
	if err := s.AssemblySpec.Validate(); err != nil {
		return err
	}
	declared := make(map[string]BootInput, len(s.Inputs))
	for i := range s.Inputs {
		declared[s.Inputs[i].Name] = s.Inputs[i]
	}
	if in, ok := declared[LaunchInputWorkDir]; !ok || !in.Required {
		return fmt.Errorf("%w: input %q must be declared and required",
			ErrLaunchSpecMissingWorkDir, LaunchInputWorkDir)
	}
	if in, ok := declared[LaunchInputRunner]; !ok || !in.Required {
		return fmt.Errorf("%w: input %q must be declared and required",
			ErrLaunchSpecMissingRunner, LaunchInputRunner)
	}
	return nil
}

// MinimumConfigInputs returns the canonical four-knob minimum-config
// input set every LaunchSpec is expected to declare: work_dir + runner
// (required) and isolation + bus (optional). The slice is a fresh copy;
// callers may mutate it freely.
func MinimumConfigInputs() []string {
	out := make([]string, len(minimumConfigInputs))
	copy(out, minimumConfigInputs)
	return out
}

// LaunchBag is one concrete invocation of a LaunchSpec — the input-bag
// file that replaces a single legacy launches/<id>.yaml. It carries the
// differing values (work dir, runner, isolation mode, bus) the spec
// renders against. The `.worktree` twin is gone: a worktree launch is
// just this same bag with isolation: worktree.
type LaunchBag struct {
	// Spec names the LaunchSpec catalog id this bag invokes.
	Spec string `yaml:"spec" json:"spec"`

	// Name is the stable identifier for this concrete launch (the value
	// that replaces the legacy launch filename stem).
	Name string `yaml:"name" json:"name"`

	// Inputs is the value bag handed to LaunchSpec.Render. Keys must be
	// inputs the referenced LaunchSpec declares. work_dir + runner are
	// mandatory (minimum config); isolation + bus are optional.
	Inputs map[string]any `yaml:"inputs,omitempty" json:"inputs,omitempty"`

	// Vars supplies already-resolved derived-var values, if the bag
	// pre-resolves any. Normally empty: vars resolve via the S4.2
	// VarResolver at render time.
	Vars map[string]any `yaml:"vars,omitempty" json:"vars,omitempty"`
}

// Validate enforces the bag-shape contract: it must name a spec and a
// stable name. Cross-checking the bag against its LaunchSpec — including
// the minimum-config requirement — is ValidateMinimumConfig, which needs
// the resolved spec.
func (b LaunchBag) Validate() error {
	if b.Spec == "" {
		return fmt.Errorf("%w: launch bag has no spec ref", ErrRegistryMissingName)
	}
	if b.Name == "" {
		return fmt.Errorf("%w: launch bag has no name", ErrRegistryMissingName)
	}
	return nil
}

// ValidateMinimumConfig checks a LaunchBag against its LaunchSpec and
// enforces the S4.4 minimum-valid-config rule:
//
//	A launch is valid with ONLY work dir + runner supplied. Isolation
//	and bus are the only other knobs and are optional. Anything else is
//	either derived or carries a spec default.
//
// It also rejects a bag key that the spec does not declare — a silent
// unknown key would render as nothing and mask a typo.
//
// A required input on the spec that has a Default is satisfied without
// the bag supplying it; only a required input with NO default and NO
// bag value is a minimum-config violation.
func ValidateMinimumConfig(spec LaunchSpec, bag LaunchBag) error {
	if err := bag.Validate(); err != nil {
		return err
	}
	declared := make(map[string]BootInput, len(spec.Inputs))
	for i := range spec.Inputs {
		declared[spec.Inputs[i].Name] = spec.Inputs[i]
	}
	for key := range bag.Inputs {
		if _, ok := declared[key]; !ok {
			return fmt.Errorf("%w: %s", ErrLaunchSpecUnknownInput, key)
		}
	}
	if !satisfied(declared, bag, LaunchInputWorkDir) {
		return fmt.Errorf("%w: bag %q supplies no %q and the spec sets no default",
			ErrLaunchSpecMissingWorkDir, bag.Name, LaunchInputWorkDir)
	}
	if !satisfied(declared, bag, LaunchInputRunner) {
		return fmt.Errorf("%w: bag %q supplies no %q and the spec sets no default",
			ErrLaunchSpecMissingRunner, bag.Name, LaunchInputRunner)
	}
	return nil
}

// satisfied reports whether input name has a usable value for bag: either
// the bag supplies it, or the spec declares a non-nil default.
func satisfied(declared map[string]BootInput, bag LaunchBag, name string) bool {
	if v, ok := bag.Inputs[name]; ok && v != nil && v != "" {
		return true
	}
	if in, ok := declared[name]; ok && in.Default != nil {
		return true
	}
	return false
}

// RenderRequest builds the S4.1 RenderRequest for invoking spec with this
// bag. It is the bridge from the S4.4 bag model to the S4.1 renderer:
// callers pass the result straight to LaunchSpec.Render.
func (b LaunchBag) RenderRequest(onMissing MissingPolicy) RenderRequest {
	return RenderRequest{
		Inputs:    b.Inputs,
		Vars:      b.Vars,
		OnMissing: onMissing,
	}
}

// LoadLaunchSpec reads and validates a LaunchSpec YAML document from
// path. The S4.5 parity harness uses this to load the re-expressed
// specs.
func LoadLaunchSpec(path string) (LaunchSpec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return LaunchSpec{}, fmt.Errorf("agentlaunch: read launch spec %q: %w", path, err)
	}
	var spec LaunchSpec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return LaunchSpec{}, fmt.Errorf("agentlaunch: parse launch spec %q: %w", path, err)
	}
	if err := spec.Validate(); err != nil {
		return LaunchSpec{}, fmt.Errorf("agentlaunch: invalid launch spec %q: %w", path, err)
	}
	return spec, nil
}

// LoadLaunchBag reads and validates a LaunchBag YAML document from path.
func LoadLaunchBag(path string) (LaunchBag, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return LaunchBag{}, fmt.Errorf("agentlaunch: read launch bag %q: %w", path, err)
	}
	var bag LaunchBag
	if err := yaml.Unmarshal(raw, &bag); err != nil {
		return LaunchBag{}, fmt.Errorf("agentlaunch: parse launch bag %q: %w", path, err)
	}
	if err := bag.Validate(); err != nil {
		return LaunchBag{}, fmt.Errorf("agentlaunch: invalid launch bag %q: %w", path, err)
	}
	return bag, nil
}
