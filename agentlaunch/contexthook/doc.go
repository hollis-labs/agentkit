// Package contexthook bridges go-agent-launch's ContextHook extension
// point to the go-agent-context assembly pipeline.
//
// # Why a subpackage
//
// agentlaunch declares the hook TYPE (ContextHook) but ships no concrete
// implementation — by design. Per the boundary documented in
// agentlaunch/hooks.go, the default behaviour is "no hook registered →
// empty boot prompt." A concrete implementation belongs OUTSIDE the
// top-level agentlaunch package so the launch contract does not gain a
// reverse dependency on context assembly.
//
// This subpackage supplies that implementation, backed by the sibling
// agentkit/agentcontext package. The adapter is a one-way bridge:
// contexthook IMPORTS agentcontext but agentcontext NEVER imports any
// agentlaunch type. The acyclic dependency is enforced by the package
// layout — agentcontext has no Go files that name agentlaunch.
//
// # Usage
//
// The common case is a one-liner over the default eight-resolver set:
//
//	resolverMap := resolvers.WithSkillIndex(resolvers.Default())
//	provider, _ := agentcontext.NewProvider(resolverMap, agentcontext.DefaultRenderer{})
//	hook := contexthook.New(provider, contexthook.Config{
//	    SlotExtractor: myExtractor,    // see Config.SlotExtractor
//	    PlantArtifacts: true,          // optional, for debugging
//	})
//	prepared, _ := launcher.Prepare(ctx, compiled, launcher.WithContextHook(hook))
//
// See contexthook.New for the contract between hook → provider → bootdir.
package contexthook
