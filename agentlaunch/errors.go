package agentlaunch

import "errors"

// Sentinel errors returned by the Validate methods on LaunchPlan,
// CompiledLaunch, and PreparedLaunch. All sentinels are
// errors.Is-comparable; callers branch on them with errors.Is rather
// than string match.
//
// The errors document which field failed; they do not carry the value
// itself. Validation that needs to report a specific offending value
// wraps the sentinel with fmt.Errorf("%w: %s", sentinel, value).
var (
	// ErrMissingProjectID is returned when LaunchPlan.Project.ID is empty.
	ErrMissingProjectID = errors.New("agentlaunch: missing project id")

	// ErrMissingAgentID is returned when LaunchPlan.Agent.ID is empty.
	ErrMissingAgentID = errors.New("agentlaunch: missing agent id")

	// ErrMissingProviderID is returned when LaunchPlan.Provider.ID is empty.
	ErrMissingProviderID = errors.New("agentlaunch: missing provider id")

	// ErrUnsupportedWorkspaceMode is returned when LaunchPlan.Workspace.Mode
	// is not one of the four declared WorkspaceMode values.
	ErrUnsupportedWorkspaceMode = errors.New("agentlaunch: unsupported workspace mode")

	// ErrUnsupportedLaunchMode is returned when LaunchPlan.Mode is not one
	// of the three declared LaunchMode values.
	ErrUnsupportedLaunchMode = errors.New("agentlaunch: unsupported launch mode")

	// ErrUnknownRuntime is returned when LaunchPlan.Runtime is not one of
	// the four declared RuntimeKind values. This is a value-level check;
	// the provider × runtime support matrix (which legal (provider, runtime)
	// pairs exist) lives in a sibling package and is NOT enforced here.
	ErrUnknownRuntime = errors.New("agentlaunch: unknown runtime kind")

	// ErrMissingBootProfile is returned when neither
	// LaunchPlan.BootProfile.CatalogPath nor LaunchPlan.BootProfile.Inline
	// is set.
	ErrMissingBootProfile = errors.New("agentlaunch: boot profile must set CatalogPath or Inline")

	// ErrUnsupportedBootMode is returned when an inline boot profile's
	// BootMode is not one of "none", "stdin", or "planted".
	ErrUnsupportedBootMode = errors.New("agentlaunch: unsupported inline boot mode")

	// ErrUnsafeInjectionTarget is returned when InjectionSpec.BootDirOverlay
	// contains a key that escapes the bootdir: an absolute path, a path
	// containing ".." segments, or a path that targets a reserved name
	// like ".git/". Path-safety is enforced defensively at validation
	// time so consumers cannot trick the preparer into writing outside
	// the planted bootdir.
	ErrUnsafeInjectionTarget = errors.New("agentlaunch: unsafe injection overlay target")

	// ErrCompiledMissingPlan is returned by CompiledLaunch.Validate when
	// the embedded Plan has not been populated.
	ErrCompiledMissingPlan = errors.New("agentlaunch: compiled launch has no source plan")

	// ErrPreparedMissingBootDir is returned by PreparedLaunch.Validate
	// when PlantedBootDir is empty — the preparer must have materialized
	// a bootdir before a PreparedLaunch is considered valid.
	ErrPreparedMissingBootDir = errors.New("agentlaunch: prepared launch missing planted bootdir")

	// ErrPreparedMissingWorkspaceDir is returned by PreparedLaunch.Validate
	// when WorkspaceDir is empty.
	ErrPreparedMissingWorkspaceDir = errors.New("agentlaunch: prepared launch missing workspace dir")

	// ErrPreparedMissingArgv is returned by PreparedLaunch.Validate when
	// Argv has length zero — at minimum the spawned binary must be named.
	ErrPreparedMissingArgv = errors.New("agentlaunch: prepared launch missing argv")

	// ErrUnknownNativeFileKind is returned by NativeFile.Validate when
	// NativeFile.Kind is not one of the declared NativeFileKind values.
	ErrUnknownNativeFileKind = errors.New("agentlaunch: unknown native file kind")

	// ErrNativeFileMissingID is returned by NativeFile.Validate when a
	// NativeFileSkill entry has an empty ID — the ID is required because
	// it derives the planted filename (.claude/skills/<ID>.md etc.).
	ErrNativeFileMissingID = errors.New("agentlaunch: native skill file missing id")

	// ErrNativeFileUnsafeID is returned by NativeFile.Validate when a
	// NativeFileSkill entry's ID is not a safe single path segment (it
	// contains a separator, a "." / ".." traversal token, or a rune
	// outside [A-Za-z0-9._-]).
	ErrNativeFileUnsafeID = errors.New("agentlaunch: native skill file id is not a safe path segment")

	// ErrNativeFileMissingRelPath is returned by NativeFile.Validate when
	// a NativeFileRaw entry has an empty RelPath.
	ErrNativeFileMissingRelPath = errors.New("agentlaunch: native raw file missing relpath")

	// ErrRuntimeBindingMissingProvider is returned by RuntimeBinding.Validate
	// when Provider is empty.
	ErrRuntimeBindingMissingProvider = errors.New("agentlaunch: runtime binding missing provider")

	// ErrRuntimeBindingInvalidTimeout is returned by
	// RuntimeBinding.Validate when Timeout is non-empty and not a valid Go
	// duration string.
	ErrRuntimeBindingInvalidTimeout = errors.New("agentlaunch: runtime binding timeout is invalid")

	// ErrBootInputMissingName is returned by BootInput.Validate when Name
	// is empty.
	ErrBootInputMissingName = errors.New("agentlaunch: boot input missing name")

	// ErrBootInputMissingType is returned by BootInput.Validate when Type
	// is empty.
	ErrBootInputMissingType = errors.New("agentlaunch: boot input missing type")

	// ErrBootSpecDuplicateInput is returned by BootSpec.Validate when two
	// inputs share the same Name.
	ErrBootSpecDuplicateInput = errors.New("agentlaunch: boot spec has duplicate input name")

	// ErrContractObjectUnknownKind is returned by ContractObject.Validate
	// when Kind is not one of the declared ContractObjectKind values.
	ErrContractObjectUnknownKind = errors.New("agentlaunch: contract object kind is unknown")

	// ErrContractObjectMissingRef is returned by ContractObject.Validate
	// when an input / var / slot object omits Ref.
	ErrContractObjectMissingRef = errors.New("agentlaunch: contract object missing ref")

	// ErrBootFileMissingID is returned by BootFileSpec.Validate when ID is
	// empty.
	ErrBootFileMissingID = errors.New("agentlaunch: boot file missing id")

	// ErrBootFileMissingRelPath is returned by BootFileSpec.Validate when
	// RelPath is empty.
	ErrBootFileMissingRelPath = errors.New("agentlaunch: boot file missing relpath")

	// ErrBootInjectionMissingID is returned by BootInjectionSpec.Validate
	// when ID is empty.
	ErrBootInjectionMissingID = errors.New("agentlaunch: boot injection missing id")

	// ErrBootInjectionMissingName is returned by BootInjectionSpec.Validate
	// when a skill-targeted injection omits Name.
	ErrBootInjectionMissingName = errors.New("agentlaunch: boot injection missing name")

	// ErrBootSpecVarMissingName is returned by VarSpec.Validate when Name
	// is empty.
	ErrBootSpecVarMissingName = errors.New("agentlaunch: boot var missing name")

	// ErrBootSpecVarUnknownSourceKind is returned by VarSource.Validate
	// when Kind is not one of the declared VarSourceKind values.
	ErrBootSpecVarUnknownSourceKind = errors.New("agentlaunch: boot var source kind is unknown")

	// ErrBootSpecVarMissingSourceConfig is returned by VarSource.Validate
	// when the selected source kind omits its required config block.
	ErrBootSpecVarMissingSourceConfig = errors.New("agentlaunch: boot var source config is missing")

	// ErrBootSpecVarUnknownFreshness is returned by VarSpec.Validate when
	// Freshness is not one of the declared VarFreshness values.
	ErrBootSpecVarUnknownFreshness = errors.New("agentlaunch: boot var freshness is unknown")

	// ErrBootSpecVarUnknownOnError is returned by VarSpec.Validate when
	// OnError is not one of the declared VarOnError values.
	ErrBootSpecVarUnknownOnError = errors.New("agentlaunch: boot var on_error is unknown")

	// ErrBootSpecVarUnknownPhase is returned by VarSpec.Validate when
	// Phase is not one of the declared MaterializationPhase values.
	ErrBootSpecVarUnknownPhase = errors.New("agentlaunch: boot var phase is unknown")

	// ErrBootSpecVarSecretInlineValue is returned by VarSpec.Validate when
	// a secret-bearing var attempts to persist an inline literal or
	// fallback value in the blueprint.
	ErrBootSpecVarSecretInlineValue = errors.New("agentlaunch: secret boot var may not persist inline values")

	// ErrBootSpecVarTrustGateRequired is returned by VarSource.Validate
	// when a call/cmd source omits both trust and authorization gates.
	ErrBootSpecVarTrustGateRequired = errors.New("agentlaunch: boot var source requires trust or authorization gate")

	// ErrBootSpecVarInvalidTimeout is returned by VarSource.Validate when
	// a call/cmd source timeout is non-empty and not a valid Go duration
	// string.
	ErrBootSpecVarInvalidTimeout = errors.New("agentlaunch: boot var source timeout is invalid")

	// ErrRegistryUnknownKind is returned when a directory registry
	// contract or reference names an unknown kind.
	ErrRegistryUnknownKind = errors.New("agentlaunch: registry kind is unknown")

	// ErrRegistryMissingName is returned when a directory registry
	// contract or reference omits its stable name.
	ErrRegistryMissingName = errors.New("agentlaunch: registry name is missing")

	// ErrRegistryMissingSchemaVersion is returned when a directory
	// registry contract omits its schema version token.
	ErrRegistryMissingSchemaVersion = errors.New("agentlaunch: registry schema version is missing")

	// ErrRegistryMissingInterface is returned when a directory registry
	// contract omits its interface token.
	ErrRegistryMissingInterface = errors.New("agentlaunch: registry interface is missing")

	// ErrRegistryUnknownOperation is returned when a registry envelope
	// names an unknown operation.
	ErrRegistryUnknownOperation = errors.New("agentlaunch: registry operation is unknown")

	// ErrRegistryUnknownResolution is returned when a registry envelope
	// names an unknown resolution policy.
	ErrRegistryUnknownResolution = errors.New("agentlaunch: registry resolution policy is unknown")

	// ErrRegistryMissingPayload is returned when a registry envelope does
	// not carry the payload required for its operation.
	ErrRegistryMissingPayload = errors.New("agentlaunch: registry envelope payload is missing")

	// ErrRegistryMissingLocalRef is returned when a local-first
	// registration omits its file-backed source of truth.
	ErrRegistryMissingLocalRef = errors.New("agentlaunch: registry local file ref is missing")

	// ErrRegistryResolverMissingHandle is returned when a resolver-backed
	// contract omits its resolver handle.
	ErrRegistryResolverMissingHandle = errors.New("agentlaunch: registry resolver handle is missing")

	// ErrRegistryResolverUnknownProtocol is returned when a resolver
	// handle names an unknown protocol.
	ErrRegistryResolverUnknownProtocol = errors.New("agentlaunch: registry resolver protocol is unknown")

	// ErrRegistryResolverMissingTarget is returned when a resolver handle
	// omits its target or command.
	ErrRegistryResolverMissingTarget = errors.New("agentlaunch: registry resolver target is missing")

	// ErrRegistryTransportUnknown is returned when an MCP server contract
	// names an unknown transport.
	ErrRegistryTransportUnknown = errors.New("agentlaunch: registry transport is unknown")

	// ErrRegistryUnknownHealthStatus is returned when a health envelope
	// names an unknown status value.
	ErrRegistryUnknownHealthStatus = errors.New("agentlaunch: registry health status is unknown")

	// ErrRegistryDuplicateObject is returned by the registrar when a
	// register operation targets a RegistryObjectRef that already exists
	// and RegisterPayload.Upsert is false.
	ErrRegistryDuplicateObject = errors.New("agentlaunch: registry object already registered")

	// ErrRegistryObjectNotFound is returned by the registrar when a
	// deregister operation targets a RegistryObjectRef that is not
	// present in the store.
	ErrRegistryObjectNotFound = errors.New("agentlaunch: registry object not found")

	// ErrRegistryUnsupportedOperation is returned by the registrar when an
	// envelope names an operation the live service does not implement.
	ErrRegistryUnsupportedOperation = errors.New("agentlaunch: registry operation is not supported")

	// ErrRegistryKindSchemaMismatch is returned by per-kind registration
	// validation when a RegistrationRecord's Meta.SchemaVersion does not
	// equal the published schema-version constant for its kind.
	ErrRegistryKindSchemaMismatch = errors.New("agentlaunch: registry record schema version does not match kind")

	// ErrRegistryKindInterfaceMismatch is returned by per-kind registration
	// validation when a RegistrationRecord's Meta.Interface does not equal
	// the published interface constant for its kind.
	ErrRegistryKindInterfaceMismatch = errors.New("agentlaunch: registry record interface does not match kind")

	// ErrRegistryKindDecode is returned by DecodeContract when a raw
	// contract document cannot be unmarshalled into the contract struct
	// for its declared kind.
	ErrRegistryKindDecode = errors.New("agentlaunch: registry contract document failed to decode")

	// ErrRegistryCacheMiss is returned by the degrading registrar wrapper
	// (S3.4) when the inner registrar is unreachable for a query AND the
	// last-known-good cache holds no entry for that query. It is the
	// terminal degradation error: the wrapper served neither a live nor a
	// cached result. Callers branch on it to distinguish "directory down
	// but cache covered us" (no error) from "directory down and we never
	// cached this query" (this error).
	ErrRegistryCacheMiss = errors.New("agentlaunch: registry degraded and no last-known-good cache entry")

	// ErrRegistryWriteWhileDegraded is returned by the degrading registrar
	// wrapper (S3.4) when a register or deregister envelope is dispatched
	// while the inner registrar is unreachable. Writes cannot be served
	// from the last-known-good cache: the cache is a read-side fallback
	// only. The error wraps the underlying inner failure so callers can
	// inspect the root cause with errors.Is / errors.Unwrap.
	ErrRegistryWriteWhileDegraded = errors.New("agentlaunch: registry write rejected while directory unreachable")

	// ErrRegistryBusTopicMissingTopic is returned by BusTopicContract.Validate
	// when the topic name/address on the bus is empty. The contract is a
	// handle to a bus topic; without the topic address it is unresolvable.
	ErrRegistryBusTopicMissingTopic = errors.New("agentlaunch: registry bus topic name is missing")

	// ErrRegistryBusTopicUnknownDirection is returned by BusTopicContract.Validate
	// when Direction is not one of the declared BusTopicDirection values.
	ErrRegistryBusTopicUnknownDirection = errors.New("agentlaunch: registry bus topic direction is unknown")

	// ErrRegistryCatalogRootUnreadable is returned by the file-backed
	// registrar (S3.3) when the catalog root supplied to IngestCatalog
	// does not exist, is not a directory, or cannot be read. It is the
	// only failure that aborts an ingest; per-file failures are recorded
	// in the IngestReport instead.
	ErrRegistryCatalogRootUnreadable = errors.New("agentlaunch: registry catalog root is unreadable")

	// ErrRegistryCatalogFileUnreadable is returned by the file-backed
	// registrar (S3.3) when an individual catalog file cannot be read off
	// disk. It is recorded as a per-file IngestError and does not abort
	// the ingest.
	ErrRegistryCatalogFileUnreadable = errors.New("agentlaunch: registry catalog file is unreadable")

	// ErrRegistryCatalogEntryMalformed is returned by the file-backed
	// registrar (S3.3) when a catalog file is not valid YAML. It is
	// recorded as a per-file IngestError and does not abort the ingest.
	ErrRegistryCatalogEntryMalformed = errors.New("agentlaunch: registry catalog entry is malformed")

	// ErrRegistryCatalogEntryMissingID is returned by the file-backed
	// registrar (S3.3) when a catalog file parses but carries no `id:`
	// field — the entry has no stable name to register under. It is
	// recorded as a per-file IngestError and does not abort the ingest.
	ErrRegistryCatalogEntryMissingID = errors.New("agentlaunch: registry catalog entry has no id")

	// ErrAssemblyMalformedTemplate is returned by the Boot Assembly Spec
	// engine (S4.1) when an AssemblySpec template body contains a
	// syntactically invalid merge tag: an unterminated "{{ ... }}", an
	// empty tag, a tag missing the scope.name form, or an unsafe name.
	ErrAssemblyMalformedTemplate = errors.New("agentlaunch: assembly template merge tag is malformed")

	// ErrAssemblyUnknownMergeTag is returned by the Boot Assembly Spec
	// engine (S4.1) when a template merge tag references an input or var
	// that the embedded BootSpec does not declare.
	ErrAssemblyUnknownMergeTag = errors.New("agentlaunch: assembly template references an undeclared input or var")

	// ErrAssemblyMissingRequiredInput is returned by AssemblySpec.Render
	// for the autonomous front-end when a declared-required input has no
	// supplied value and no default — there is no human to collect it
	// from. The interactive front-end reports the same condition via
	// RenderResult.Missing instead of returning this error.
	ErrAssemblyMissingRequiredInput = errors.New("agentlaunch: assembly render missing required input")
)
