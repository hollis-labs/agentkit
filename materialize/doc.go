// Package materialize defines the shared filesystem planning, write and
// ownership contracts for agentkit artifacts.
//
// The default engine validates a complete neutral artifact tree before any
// destination mutation. Create writes into a private staging directory under an
// os.Root opened on the target parent and publishes with Root.Rename only after
// every entry has been written. The Unix threat model rejects symlinks anywhere
// in the destination parent chain before opening that parent root, then relies on
// os.Root methods to keep subsequent path traversal and path-swap races inside
// the opened root. Platforms or modes that cannot uphold those guarantees should
// return an unsupported or unsafe-target error instead of silently weakening the
// boundary.
//
// Reconcile and refresh operate on existing mixed-ownership directories using a
// saved manifest under .agentkit/materialize-manifest.json. They preflight owned
// paths and managed keys before mutation, preserve unowned content, and apply
// each file with atomic replacement. They intentionally do not claim
// whole-directory transaction semantics for mixed homes; interrupted multi-file
// writes return an incomplete report and leave the previous manifest in place.
package materialize
