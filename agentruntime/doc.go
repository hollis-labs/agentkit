// Package agentruntime is a small facade for the
// github.com/hollis-labs/agentkit/agentruntime module.
//
// The implementation surface intentionally lives in focused subpackages:
// runtimekind, runtimebind, turn, sessionkit, bootdir, loopback, and
// checkpoint. Keeping the root package small avoids a grab-bag API while still
// giving documentation tooling a stable module overview.
package agentruntime
