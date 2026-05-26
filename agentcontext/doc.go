// Package agentcontext provides a portfolio-shared mechanical context
// assembly substrate for agent runtimes. It defines the ContextRequest →
// ContextResult pipeline that turns a declarative, ordered list of typed
// slot specifications plus byte/token budgets and caller-supplied
// provenance into a deterministic boot-prompt body suitable for
// injection into a go-agent-launch PreparedLaunch.
//
// The package sits beside agentkit/agentlaunch (the launch substrate)
// and below the app-specific orchestrators (Tether, Torque, Nanite)
// that each previously grew their own near-identical context-assembly
// pipelines. It owns:
//
//   - the ContextRequest / ContextResult types and the high-level
//     ContextProvider entry point that walks the slot list in order,
//   - the typed SlotSource tagged union (static_file, static_dir,
//     inline, cmd, http_text, http_json, role_summary, skill_index) —
//     the public vocabulary every concrete resolver must speak,
//   - the Resolver and Renderer extension-point interfaces and a
//     default section-headered, input-order Renderer implementation,
//   - the Limits / LimitsApplied byte- and token-budget contract — the
//     token estimate uses a documented char/4 heuristic; precise
//     tokenization is the caller's problem,
//   - the Provenance / SlotProvenance shape — caller-supplied request
//     attribution flows through unchanged; the library only adds the
//     library version, the request content hash, and per-slot resolver
//     kind / byte size / content hash / fetched-at timestamps,
//   - validation sentinels — every error path is an errors.Is
//     comparable sentinel, never a fmt-string-only error.
//
// # Determinism contract
//
// Identical ContextRequests (same slot order, same SlotSpec values,
// same Limits, same Workdir, same caller Provenance) MUST produce
// byte-identical Rendered output and byte-identical RequestHash digests.
// The library enforces this by:
//
//   - walking req.Slots in slice order — never alphabetical, never
//     map-iteration order,
//   - canonicalizing the request via JSON with lexicographically sorted
//     map keys before hashing (defensive against future stdlib map
//     ordering changes),
//   - emitting section headers and slot bodies in input order.
//
// Concrete resolvers MAY introduce non-determinism (a cmd resolver
// shelling out to `date`, an http_text resolver hitting a clock-skew
// endpoint) — that is a resolver-side concern, NOT a library concern.
// The library hashes the REQUEST, not the resolved output, so the
// caching contract holds regardless of resolver determinism.
//
// # App-neutrality
//
// The package imports only the Go standard library and gopkg.in/yaml.v3
// (for the boot-profile YAML shape). It does not import agentkit/agentlaunch,
// agentkit/agentsessions, go-providers, Tether, Torque, or Nanite. It
// makes no Vanta calls and no MCP calls. The caller is responsible for
// routing resolved Vanta / MCP / app output into an `inline` slot if a
// slot needs that input.
package agentcontext
