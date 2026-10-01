// Package agentruntime is a small facade for the
// github.com/hollis-labs/agentkit/agentruntime module.
//
// The implementation surface intentionally lives in focused subpackages:
// runtimebind, turn, sessionkit, bootdir, loopback, and checkpoint. Runtime
// ids and modes are the agent-contracts-leaf runtimes vocabulary; there is no
// runtime-kind package here. Keeping the root package small avoids a grab-bag API while still
// giving documentation tooling a stable module overview.
package agentruntime
