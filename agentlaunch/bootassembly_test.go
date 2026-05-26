package agentlaunch

import (
	"errors"
	"reflect"
	"testing"
)

// sampleAssembly builds a representative AssemblySpec exercising typed
// inputs (required, defaulted, plain), a var, and a merge-tag body.
func sampleAssembly() AssemblySpec {
	return AssemblySpec{
		BootSpec: BootSpec{
			Inputs: []BootInput{
				{Name: "ticket", Type: "string", Required: true, Description: "task id"},
				{Name: "role", Type: "string", Default: "backend", Description: "agent role"},
				{Name: "verbose", Type: "bool", Default: false},
			},
			Vars: []VarSpec{
				{
					Name:      "role_summary",
					Source:    VarSource{Kind: VarSourceFile, File: &VarFileRef{Path: "roles/backend.md"}},
					Freshness: VarFreshnessCacheOK,
					OnError:   VarOnErrorAbort,
					Phase:     VarPhaseBuild,
				},
			},
			Runtime: RuntimeBinding{Provider: "codex", RuntimeKind: RuntimePTY},
		},
		Template: "Ticket: {{ inputs.ticket }}\nRole: {{inputs.role}}\nVerbose: {{ inputs.verbose }}\nSummary: {{ vars.role_summary }}\n",
	}
}

func TestAssemblySpecValidate(t *testing.T) {
	a := sampleAssembly()
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestAssemblySpecValidateRejectsUnknownMergeTag(t *testing.T) {
	a := sampleAssembly()
	a.Template = "Hello {{ inputs.nonexistent }}"
	err := a.Validate()
	if !errors.Is(err, ErrAssemblyUnknownMergeTag) {
		t.Fatalf("Validate() error = %v, want ErrAssemblyUnknownMergeTag", err)
	}
}

func TestAssemblySpecValidateRejectsMalformedTemplate(t *testing.T) {
	cases := map[string]string{
		"unterminated": "value is {{ inputs.ticket ",
		"empty tag":    "value is {{   }}",
		"no scope":     "value is {{ ticket }}",
		"bad scope":    "value is {{ secrets.ticket }}",
		"unsafe name":  "value is {{ inputs.tic.ket }}",
	}
	for name, tmpl := range cases {
		t.Run(name, func(t *testing.T) {
			a := sampleAssembly()
			a.Template = tmpl
			err := a.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want error for %q", tmpl)
			}
			// "no scope"/"bad scope"/"unsafe name" are malformed-template
			// failures; "unterminated"/"empty" likewise.
			if !errors.Is(err, ErrAssemblyMalformedTemplate) {
				t.Fatalf("Validate() error = %v, want ErrAssemblyMalformedTemplate", err)
			}
		})
	}
}

func TestRenderDeterministic(t *testing.T) {
	a := sampleAssembly()
	req := RenderRequest{
		Inputs:   map[string]any{"ticket": "CW-20260517-0026"},
		Vars:     map[string]any{"role_summary": "Backend engineer."},
		FrontEnd: FrontEndAutonomous,
	}
	first, err := a.Render(req)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	for i := 0; i < 25; i++ {
		got, err := a.Render(req)
		if err != nil {
			t.Fatalf("Render() iteration %d error = %v", i, err)
		}
		if got.Body != first.Body {
			t.Fatalf("Render() not deterministic: iteration %d body = %q, want %q", i, got.Body, first.Body)
		}
		if !reflect.DeepEqual(got.ResolvedInputs, first.ResolvedInputs) {
			t.Fatalf("Render() ResolvedInputs not deterministic at iteration %d", i)
		}
	}
	want := "Ticket: CW-20260517-0026\nRole: backend\nVerbose: false\nSummary: Backend engineer.\n"
	if first.Body != want {
		t.Fatalf("Render() body = %q, want %q", first.Body, want)
	}
}

func TestRenderSuppliedInputOverridesDefault(t *testing.T) {
	a := sampleAssembly()
	res, err := a.Render(RenderRequest{
		Inputs:   map[string]any{"ticket": "CW-1", "role": "frontend", "verbose": true},
		Vars:     map[string]any{"role_summary": "x"},
		FrontEnd: FrontEndAutonomous,
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if res.ResolvedInputs["role"] != "frontend" {
		t.Fatalf("supplied role not honored: got %v", res.ResolvedInputs["role"])
	}
	if res.ResolvedInputs["verbose"] != true {
		t.Fatalf("supplied verbose not honored: got %v", res.ResolvedInputs["verbose"])
	}
}

func TestRenderAutonomousErrorsOnMissingRequiredInput(t *testing.T) {
	a := sampleAssembly()
	res, err := a.Render(RenderRequest{
		// ticket (required, no default) omitted.
		Vars:     map[string]any{"role_summary": "x"},
		FrontEnd: FrontEndAutonomous,
	})
	if !errors.Is(err, ErrAssemblyMissingRequiredInput) {
		t.Fatalf("Render() error = %v, want ErrAssemblyMissingRequiredInput", err)
	}
	if res.Body != "" {
		t.Fatalf("Render() body = %q, want empty on autonomous failure", res.Body)
	}
	if len(res.Missing) != 1 || res.Missing[0] != "inputs.ticket" {
		t.Fatalf("Render() Missing = %v, want [inputs.ticket]", res.Missing)
	}
}

func TestRenderInteractiveCollectsMissingRequiredInput(t *testing.T) {
	a := sampleAssembly()
	res, err := a.Render(RenderRequest{
		// ticket omitted.
		Vars:     map[string]any{"role_summary": "x"},
		FrontEnd: FrontEndInteractive,
	})
	if err != nil {
		t.Fatalf("Render() error = %v, want nil for interactive front-end", err)
	}
	if len(res.Missing) != 1 || res.Missing[0] != "inputs.ticket" {
		t.Fatalf("Render() Missing = %v, want [inputs.ticket]", res.Missing)
	}
	// Body still renders; the missing tag collapses to empty string.
	want := "Ticket: \nRole: backend\nVerbose: false\nSummary: x\n"
	if res.Body != want {
		t.Fatalf("Render() body = %q, want %q", res.Body, want)
	}
}

func TestRenderReportsMissingVar(t *testing.T) {
	a := sampleAssembly()
	res, err := a.Render(RenderRequest{
		Inputs: map[string]any{"ticket": "CW-1"},
		// role_summary var not supplied.
		FrontEnd: FrontEndInteractive,
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if len(res.Missing) != 1 || res.Missing[0] != "vars.role_summary" {
		t.Fatalf("Render() Missing = %v, want [vars.role_summary]", res.Missing)
	}
}

func TestRenderMissingOrderingDeterministic(t *testing.T) {
	a := AssemblySpec{
		BootSpec: BootSpec{
			Inputs: []BootInput{
				{Name: "zeta", Type: "string", Required: true},
				{Name: "alpha", Type: "string", Required: true},
			},
			Vars: []VarSpec{
				{
					Name:      "wvar",
					Source:    VarSource{Kind: VarSourceLiteral, Literal: "x"},
					Freshness: VarFreshnessCacheOK,
					OnError:   VarOnErrorAbort,
					Phase:     VarPhaseBuild,
				},
				{
					Name:      "avar",
					Source:    VarSource{Kind: VarSourceLiteral, Literal: "x"},
					Freshness: VarFreshnessCacheOK,
					OnError:   VarOnErrorAbort,
					Phase:     VarPhaseBuild,
				},
			},
			Runtime: RuntimeBinding{Provider: "codex", RuntimeKind: RuntimePTY},
		},
		Template: "{{ inputs.zeta }}{{ inputs.alpha }}{{ vars.wvar }}{{ vars.avar }}",
	}
	res, err := a.Render(RenderRequest{FrontEnd: FrontEndInteractive})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	// Declared-input order first (zeta, alpha), then vars sorted lexically.
	want := []string{"inputs.zeta", "inputs.alpha", "vars.avar", "vars.wvar"}
	if !reflect.DeepEqual(res.Missing, want) {
		t.Fatalf("Render() Missing = %v, want %v", res.Missing, want)
	}
}

func TestRenderEscapedBraces(t *testing.T) {
	a := AssemblySpec{
		BootSpec: BootSpec{
			Inputs:  []BootInput{{Name: "name", Type: "string", Default: "world"}},
			Runtime: RuntimeBinding{Provider: "codex", RuntimeKind: RuntimePTY},
		},
		Template: "literal {{{{ inputs.name }}}} and real {{ inputs.name }}",
	}
	res, err := a.Render(RenderRequest{FrontEnd: FrontEndAutonomous})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	want := "literal {{ inputs.name }} and real world"
	if res.Body != want {
		t.Fatalf("Render() body = %q, want %q", res.Body, want)
	}
}

func TestRenderOneSpecManyInvocations(t *testing.T) {
	// The S4.1 thesis: one Spec, many invocations via inputs.
	a := sampleAssembly()
	invocations := []map[string]any{
		{"ticket": "CW-1", "role": "backend"},
		{"ticket": "CW-2", "role": "frontend"},
		{"ticket": "CW-3"},
	}
	seen := make(map[string]struct{})
	for _, inv := range invocations {
		res, err := a.Render(RenderRequest{
			Inputs:   inv,
			Vars:     map[string]any{"role_summary": "s"},
			FrontEnd: FrontEndAutonomous,
		})
		if err != nil {
			t.Fatalf("Render(%v) error = %v", inv, err)
		}
		if _, dup := seen[res.Body]; dup {
			t.Fatalf("Render(%v) produced a non-unique body — inputs did not differentiate output", inv)
		}
		seen[res.Body] = struct{}{}
	}
}

func TestCollectableInputs(t *testing.T) {
	a := sampleAssembly()

	none := a.CollectableInputs(map[string]any{"ticket": "CW-1"})
	if len(none) != 0 {
		t.Fatalf("CollectableInputs() = %v, want empty when required input supplied", none)
	}

	got := a.CollectableInputs(nil)
	if len(got) != 1 || got[0].Name != "ticket" {
		t.Fatalf("CollectableInputs(nil) = %v, want [ticket]", got)
	}
}

func TestAssemblySpecValidateInheritsBootSpecContract(t *testing.T) {
	// A bad embedded BootSpec must fail AssemblySpec.Validate too.
	a := AssemblySpec{
		BootSpec: BootSpec{
			Inputs: []BootInput{
				{Name: "dup", Type: "string"},
				{Name: "dup", Type: "string"},
			},
			Runtime: RuntimeBinding{Provider: "codex", RuntimeKind: RuntimePTY},
		},
	}
	err := a.Validate()
	if !errors.Is(err, ErrBootSpecDuplicateInput) {
		t.Fatalf("Validate() error = %v, want ErrBootSpecDuplicateInput", err)
	}
}
