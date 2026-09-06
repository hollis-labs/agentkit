package agentlaunch

import (
	"github.com/hollis-labs/go-providers/provider"

	"github.com/hollis-labs/agentkit/artifact"
)

// ProviderProjectionFromProvider translates go-providers' provider-owned pure
// projection into agentkit's neutral artifact and prepared-execution contract.
func ProviderProjectionFromProvider(in provider.ProviderProjection) ProviderProjection {
	out := ProviderProjection{
		Provider:    string(in.Provider),
		Runtime:     runtimeKindForProviderMode(in.Mode),
		Artifacts:   artifact.Tree{Entries: make([]artifact.Entry, 0, len(in.Files))},
		Effects:     make([]RuntimeEffect, 0, len(in.Effects)),
		Diagnostics: make([]CapabilityDiagnostic, 0, len(in.Diagnostics)),
	}
	for _, f := range in.Files {
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		content := append([]byte(nil), f.Content...)
		if content == nil {
			content = []byte{}
		}
		out.Artifacts.Entries = append(out.Artifacts.Entries, artifact.Entry{
			Path:  f.RelPath,
			Kind:  artifact.EntryFile,
			Mode:  mode,
			Bytes: content,
			Ownership: artifact.Ownership{
				EntryID: "provider:" + string(in.Provider) + ":" + f.RelPath,
				GroupID: "provider:" + string(in.Provider) + ":" + string(in.Mode),
			},
			Provenance: artifact.Provenance{Source: "go-providers", SourcePath: f.Role, Revision: in.Version},
		})
	}
	for _, e := range in.Effects {
		out.Effects = append(out.Effects, RuntimeEffect{
			Kind:           runtimeEffectKind(e.Kind),
			Name:           string(e.Kind),
			ProviderEffect: string(e.Kind),
			Destination:    e.Destination,
			Required:       false,
			Redacted:       providerEffectIsSecret(e.Kind),
			Owner:          string(in.Provider),
			Diagnostic:     e.Reason,
		})
	}
	for _, d := range in.Diagnostics {
		out.Diagnostics = append(out.Diagnostics, CapabilityDiagnostic{
			Code:     d.Code,
			Severity: "warning",
			Message:  d.Message,
			Feature:  string(d.Feature),
			Provider: string(in.Provider),
			Runtime:  runtimeKindForProviderMode(in.Mode),
		})
	}
	if normalized, err := artifact.Normalize(out.Artifacts.Entries); err == nil {
		out.Artifacts.Entries = normalized
	}
	return out
}

func runtimeKindForProviderMode(mode provider.ProviderMode) RuntimeKind {
	switch mode {
	case provider.ModeClaudePTY:
		return RuntimePTY
	case provider.ModeClaudeStreamingStdio:
		return RuntimeStreamingStdio
	case provider.ModeCodexAppServer:
		return RuntimeJsonRpcStdio
	case provider.ModeOpencodeServeHTTP:
		return RuntimeServeHTTP
	default:
		return RuntimeSubprocess
	}
}

func runtimeEffectKind(kind provider.ProviderEffectKind) RuntimeEffectKind {
	switch kind {
	case provider.EffectCodexAuthJSON, provider.EffectOpencodeProviderAuth:
		return RuntimeEffectCredential
	case provider.EffectClaudeWorkspaceTrust:
		return RuntimeEffectHostConfig
	case provider.EffectClaudeCredentialHelper:
		return RuntimeEffectCredential
	default:
		return RuntimeEffectCredential
	}
}

func providerEffectIsSecret(kind provider.ProviderEffectKind) bool {
	switch kind {
	case provider.EffectCodexAuthJSON, provider.EffectOpencodeProviderAuth:
		return true
	default:
		return false
	}
}
