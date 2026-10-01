package agentlaunch

import "github.com/hollis-labs/go-providers/provider"

// TurnTemplate is a provider's launch convention bound to the roots it was
// resolved against, with the caller's extra arguments. A prepared launch's
// Argv is the template's first turn, prompt included; a runtime resolves every
// turn from the template instead, so each turn carries its own prompt and
// resume id and nothing is appended after the provider's argv
// (CW-20260930-0135).
type TurnTemplate struct {
	// Convention is the provider projection's launch convention.
	Convention provider.LaunchConvention `yaml:"convention" json:"convention"`
	// Roots are the launch roots Convention's path arguments resolve
	// against.
	Roots provider.ProjectionRoots `yaml:"roots" json:"roots"`
	// ExtraArgs are the launch's own flags (LaunchPlan Provider.Flags and
	// Injection.Args). They go at the convention's extra-argument slot on
	// every turn.
	ExtraArgs []string `yaml:"extra_args,omitempty" json:"extra_args,omitempty"`
}

// TurnArgv is the argv for one turn, without the executable: Convention
// resolved for in, with ExtraArgs and then extra at its extra-argument slot.
func (t TurnTemplate) TurnArgv(in provider.TurnInput, extra ...string) ([]string, error) {
	args := append(append([]string(nil), t.ExtraArgs...), extra...)
	b, err := t.Convention.ResolveTurn(t.Roots, in, args)
	if err != nil {
		return nil, err
	}
	return b.Argv, nil
}

// BootDelivery is how a launch's boot prompt reaches the agent: the boot-mode
// token, the durable boot prompt and the per-task kickoff body, as on
// PreparedLaunch. A PreparedExecution carries it so a session shim can hand it
// on without the PreparedLaunch.
type BootDelivery struct {
	Mode    string `yaml:"mode,omitempty" json:"mode,omitempty"`
	Prompt  string `yaml:"prompt,omitempty" json:"prompt,omitempty"`
	Content string `yaml:"content,omitempty" json:"content,omitempty"`
}

// Clone returns a deep copy of t, so a holder can keep it unchanged while the
// original is edited.
func (t *TurnTemplate) Clone() *TurnTemplate {
	if t == nil {
		return nil
	}
	c := *t
	c.Convention.Argv = append([]provider.ArgTemplate(nil), t.Convention.Argv...)
	c.Convention.Env = append([]provider.EnvDelta(nil), t.Convention.Env...)
	c.ExtraArgs = append([]string(nil), t.ExtraArgs...)
	return &c
}
