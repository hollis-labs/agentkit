package agentlaunch

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ErrRuntimeBindingNotFound is returned by ResolveRuntimeBinding when the
// registry has no runtime-binding registered under the given runner id.
//
// It is a distinct sentinel so a caller can branch on it: PlanFromLaunch
// decision §4.1 requires that any fallback to a spec-baked default be a
// deliberate, observable caller choice — ResolveRuntimeBinding never falls
// back itself.
var ErrRuntimeBindingNotFound = errors.New("agentlaunch: no runtime-binding registered for runner")

// ResolveRuntimeBinding looks up the runtime-binding registered under
// runnerID and returns its RuntimeBinding, ready to hand to PlanFromLaunch
// as PlanFromLaunchInput.Runtime.
//
// It is the caller convenience the bridge design names: PlanFromLaunch is a
// pure transform and does not resolve anything — resolving the bag's
// `runner` input to a RuntimeBinding is the caller's concern (D1: resolution
// is local-first, the caller owns it). ResolveRuntimeBinding does that
// resolution against a directory registrar:
//
//  1. issues a `query` envelope for kind=runtime-binding, name=runnerID;
//  2. takes the single matching RegistrationRecord;
//  3. loads the file the record's RegistrationSource points at — the
//     registry stores handles, not content (D2), so the RuntimeBindingContract
//     body lives in that file;
//  4. decodes + validates the contract and returns its Binding.
//
// descriptor is the registrar descriptor the query envelope requires
// (Mode + FileRoot / DirectoryURL); the caller already holds it from
// configuring reg.
//
// Errors: ErrRuntimeBindingNotFound when nothing is registered for runnerID;
// a wrapped error for an ambiguous match (more than one record), an
// unreadable or malformed source file, or a contract that fails validation.
func ResolveRuntimeBinding(reg Registrar, descriptor RegistryRegistrar, runnerID string) (RuntimeBinding, error) {
	if runnerID == "" {
		return RuntimeBinding{}, fmt.Errorf("agentlaunch: ResolveRuntimeBinding: empty runner id")
	}

	resp, err := reg.Handle(RegistryEnvelope{
		Version:    RegistryEnvelopeVersionV1,
		Resolution: RegistryResolutionLocalFirst,
		Operation:  RegistryOperationQuery,
		Registrar:  descriptor,
		Query: &QueryPayload{
			Kinds: []RegistryKind{RegistryKindRuntimeBinding},
			Name:  runnerID,
		},
	})
	if err != nil {
		return RuntimeBinding{}, fmt.Errorf("agentlaunch: ResolveRuntimeBinding %q: registry query: %w", runnerID, err)
	}

	switch len(resp.Records) {
	case 0:
		return RuntimeBinding{}, fmt.Errorf("%w: %q", ErrRuntimeBindingNotFound, runnerID)
	case 1:
		// exactly one — resolve it below
	default:
		return RuntimeBinding{}, fmt.Errorf("agentlaunch: ResolveRuntimeBinding %q: ambiguous — %d runtime-binding records match",
			runnerID, len(resp.Records))
	}

	src := resp.Records[0].Source.FilePath
	raw, err := os.ReadFile(src) //nolint:gosec // registry-sourced catalog path
	if err != nil {
		return RuntimeBinding{}, fmt.Errorf("agentlaunch: ResolveRuntimeBinding %q: read source %s: %w", runnerID, src, err)
	}
	var contract RuntimeBindingContract
	if err := yaml.Unmarshal(raw, &contract); err != nil {
		return RuntimeBinding{}, fmt.Errorf("agentlaunch: ResolveRuntimeBinding %q: decode %s: %w", runnerID, src, err)
	}
	if err := contract.Validate(); err != nil {
		return RuntimeBinding{}, fmt.Errorf("agentlaunch: ResolveRuntimeBinding %q: invalid runtime-binding contract %s: %w", runnerID, src, err)
	}
	return contract.Binding, nil
}
