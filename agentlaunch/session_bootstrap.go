package agentlaunch

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
)

// CanonicalSessionEnv is the standard environment key that carries the
// provider-neutral session identity into hooks, tools, and runtime events.
const CanonicalSessionEnv = "SESSION"

// SessionBootstrapIntent declares whether a launch is preserving an existing
// continuity episode or creating a new one.
type SessionBootstrapIntent string

const (
	SessionBootstrapPreassigned SessionBootstrapIntent = "preassigned"
	SessionBootstrapResume      SessionBootstrapIntent = "resume"
	SessionBootstrapCompact     SessionBootstrapIntent = "compact"
	SessionBootstrapFresh       SessionBootstrapIntent = "fresh"
	SessionBootstrapFork        SessionBootstrapIntent = "fork"
)

func (i SessionBootstrapIntent) Valid() bool {
	switch i {
	case SessionBootstrapPreassigned, SessionBootstrapResume, SessionBootstrapCompact, SessionBootstrapFresh, SessionBootstrapFork:
		return true
	default:
		return false
	}
}

// PublicationChoice records if and how a session may be advertised outside its
// local host. The zero value resolves to private-local.
type PublicationChoice string

const (
	PublicationPrivateLocal   PublicationChoice = "private-local"
	PublicationPublishedLocal PublicationChoice = "published-local"
	PublicationTetherHosted   PublicationChoice = "tether-hosted"
)

func (p PublicationChoice) OrDefault() PublicationChoice {
	if p == "" {
		return PublicationPrivateLocal
	}
	return p
}

func (p PublicationChoice) Valid() bool {
	switch p.OrDefault() {
	case PublicationPrivateLocal, PublicationPublishedLocal, PublicationTetherHosted:
		return true
	default:
		return false
	}
}

// DefinitionRef identifies the reusable definition/profile revision that
// produced a launch. It is not a recipient address and may be absent.
type DefinitionRef struct {
	Authority string `yaml:"authority,omitempty" json:"authority,omitempty"`
	Name      string `yaml:"name,omitempty" json:"name,omitempty"`
	Revision  string `yaml:"revision,omitempty" json:"revision,omitempty"`
}

func (r DefinitionRef) IsZero() bool { return r.Authority == "" && r.Name == "" && r.Revision == "" }

func (r DefinitionRef) Validate() error {
	if r.IsZero() {
		return nil
	}
	if err := validateBootstrapSegment(r.Authority, true); err != nil {
		return fmt.Errorf("%w: definition authority", err)
	}
	if err := validateBootstrapSegment(r.Name, true); err != nil {
		return fmt.Errorf("%w: definition name", err)
	}
	if r.Revision != "" {
		if err := validateBootstrapSegment(r.Revision, false); err != nil {
			return fmt.Errorf("%w: definition revision", err)
		}
	}
	return nil
}

// ActorRef names an optional durable participant/actor. A session can exist
// without a durable actor.
type ActorRef struct {
	Authority string `yaml:"authority,omitempty" json:"authority,omitempty"`
	ID        string `yaml:"id,omitempty" json:"id,omitempty"`
	Durable   bool   `yaml:"durable,omitempty" json:"durable,omitempty"`
}

func (r ActorRef) IsZero() bool { return r.Authority == "" && r.ID == "" && !r.Durable }

func (r ActorRef) Validate() error {
	if r.IsZero() {
		return nil
	}
	if err := validateBootstrapSegment(r.Authority, true); err != nil {
		return fmt.Errorf("%w: actor authority", err)
	}
	if err := validateBootstrapSegment(r.ID, true); err != nil {
		return fmt.Errorf("%w: actor id", err)
	}
	return nil
}

// ProviderSessionMapping records a provider-native session identifier when the
// provider actually returned one. NativeSessionID may be empty while a mapping
// row still names the provider/owner scope; empty never means fabricate one.
type ProviderSessionMapping struct {
	Owner           string `yaml:"owner" json:"owner"`
	Provider        string `yaml:"provider" json:"provider"`
	NativeSessionID string `yaml:"native_session_id,omitempty" json:"native_session_id,omitempty"`
}

func (m ProviderSessionMapping) Validate() error {
	if err := validateBootstrapSegment(m.Owner, true); err != nil {
		return fmt.Errorf("%w: provider mapping owner", err)
	}
	if err := validateBootstrapSegment(m.Provider, true); err != nil {
		return fmt.Errorf("%w: provider mapping provider", err)
	}
	if m.NativeSessionID != "" {
		if err := validateBootstrapSegment(m.NativeSessionID, false); err != nil {
			return fmt.Errorf("%w: provider native session id", err)
		}
	}
	return nil
}

// RuntimeAttemptRef identifies the concrete host/runtime incarnation that will
// own delivery observations. It is metadata, not permission to take over a
// session.
type RuntimeAttemptRef struct {
	HostID            string `yaml:"host_id,omitempty" json:"host_id,omitempty"`
	BindingID         string `yaml:"binding_id,omitempty" json:"binding_id,omitempty"`
	AttemptID         string `yaml:"attempt_id,omitempty" json:"attempt_id,omitempty"`
	BindingGeneration int64  `yaml:"binding_generation,omitempty" json:"binding_generation,omitempty"`
}

func (r RuntimeAttemptRef) Validate() error {
	for label, value := range map[string]string{
		"host id":    r.HostID,
		"binding id": r.BindingID,
		"attempt id": r.AttemptID,
	} {
		if value != "" {
			if err := validateBootstrapSegment(value, false); err != nil {
				return fmt.Errorf("%w: runtime %s", err, label)
			}
		}
	}
	if r.BindingGeneration < 0 {
		return ErrBootstrapInvalidGeneration
	}
	return nil
}

// WorkRef is an opaque correlation to external work tracking, such as Torque.
type WorkRef struct {
	System string `yaml:"system" json:"system"`
	ID     string `yaml:"id" json:"id"`
}

func (r WorkRef) Validate() error {
	if err := validateBootstrapSegment(r.System, true); err != nil {
		return fmt.Errorf("%w: work system", err)
	}
	if err := validateBootstrapSegment(r.ID, true); err != nil {
		return fmt.Errorf("%w: work id", err)
	}
	return nil
}

// TraceRef is an opaque, content-free correlation pointer.
type TraceRef struct {
	Kind string `yaml:"kind" json:"kind"`
	ID   string `yaml:"id" json:"id"`
}

func (r TraceRef) Validate() error {
	if err := validateBootstrapSegment(r.Kind, true); err != nil {
		return fmt.Errorf("%w: trace kind", err)
	}
	if err := validateBootstrapSegment(r.ID, true); err != nil {
		return fmt.Errorf("%w: trace id", err)
	}
	return nil
}

// SessionBootstrap is the safe, serializable identity packet supplied to hooks,
// tools, and runtime event code.
type SessionBootstrap struct {
	SessionID        string                   `yaml:"session_id" json:"session_id"`
	SessionEnvKey    string                   `yaml:"session_env_key" json:"session_env_key"`
	Intent           SessionBootstrapIntent   `yaml:"intent" json:"intent"`
	ParentSessionID  string                   `yaml:"parent_session_id,omitempty" json:"parent_session_id,omitempty"`
	Definition       DefinitionRef            `yaml:"definition,omitempty" json:"definition,omitempty"`
	Actor            ActorRef                 `yaml:"actor,omitempty" json:"actor,omitempty"`
	Runtime          RuntimeAttemptRef        `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	ProviderMappings []ProviderSessionMapping `yaml:"provider_mappings,omitempty" json:"provider_mappings,omitempty"`
	LegacySessionIDs []string                 `yaml:"legacy_session_ids,omitempty" json:"legacy_session_ids,omitempty"`
	WorkRefs         []WorkRef                `yaml:"work_refs,omitempty" json:"work_refs,omitempty"`
	TraceRefs        []TraceRef               `yaml:"trace_refs,omitempty" json:"trace_refs,omitempty"`
	Publication      PublicationChoice        `yaml:"publication" json:"publication"`
	CreatedAt        time.Time                `yaml:"created_at" json:"created_at"`
}

func (b SessionBootstrap) Validate() error {
	if err := ValidateSessionID(b.SessionID); err != nil {
		return err
	}
	if b.SessionEnvKey != CanonicalSessionEnv {
		return ErrBootstrapInvalidSessionEnvKey
	}
	if !b.Intent.Valid() {
		return ErrBootstrapUnknownIntent
	}
	if b.ParentSessionID != "" {
		if err := ValidateSessionID(b.ParentSessionID); err != nil {
			return err
		}
		if b.ParentSessionID == b.SessionID && b.Intent == SessionBootstrapFork {
			return ErrBootstrapSessionCollision
		}
	}
	if err := b.Definition.Validate(); err != nil {
		return err
	}
	if err := b.Actor.Validate(); err != nil {
		return err
	}
	if err := b.Runtime.Validate(); err != nil {
		return err
	}
	if !b.Publication.Valid() {
		return ErrBootstrapUnknownPublication
	}
	seenMappings := map[string]struct{}{}
	for _, mapping := range b.ProviderMappings {
		if err := mapping.Validate(); err != nil {
			return err
		}
		key := mapping.Owner + "\x00" + mapping.Provider + "\x00" + mapping.NativeSessionID
		if _, ok := seenMappings[key]; ok {
			return ErrBootstrapProviderMappingCollision
		}
		seenMappings[key] = struct{}{}
	}
	seenLegacy := map[string]struct{}{}
	for _, legacy := range b.LegacySessionIDs {
		if err := ValidateSessionID(legacy); err != nil {
			return err
		}
		if legacy == b.SessionID {
			return ErrBootstrapSessionCollision
		}
		if _, ok := seenLegacy[legacy]; ok {
			return ErrBootstrapSessionCollision
		}
		seenLegacy[legacy] = struct{}{}
	}
	for _, ref := range b.WorkRefs {
		if err := ref.Validate(); err != nil {
			return err
		}
	}
	for _, ref := range b.TraceRefs {
		if err := ref.Validate(); err != nil {
			return err
		}
	}
	if b.CreatedAt.IsZero() {
		return ErrBootstrapMissingCreatedAt
	}
	return nil
}

// SessionBootstrapRequest resolves one launch's identity behavior.
type SessionBootstrapRequest struct {
	Intent           SessionBootstrapIntent
	SessionID        string
	Previous         *SessionBootstrap
	ParentSessionID  string
	Definition       DefinitionRef
	Actor            ActorRef
	Runtime          RuntimeAttemptRef
	ProviderMappings []ProviderSessionMapping
	LegacySessionIDs []string
	WorkRefs         []WorkRef
	TraceRefs        []TraceRef
	Publication      PublicationChoice
	Now              func() time.Time
	Mint             func() (string, error)
}

// PrepareSessionBootstrap returns a validated bootstrap record. Resume and
// compact preserve the previous canonical session. Fresh and fork always mint a
// new session ID; preassigned requires the caller-supplied canonical SESSION.
func PrepareSessionBootstrap(req SessionBootstrapRequest) (SessionBootstrap, error) {
	intent := req.Intent
	if intent == "" {
		intent = SessionBootstrapFresh
	}
	if !intent.Valid() {
		return SessionBootstrap{}, ErrBootstrapUnknownIntent
	}

	sessionID, err := resolveSessionID(req, intent)
	if err != nil {
		return SessionBootstrap{}, err
	}

	now := time.Now().UTC()
	if req.Now != nil {
		now = req.Now().UTC()
	}
	out := SessionBootstrap{
		SessionID:        sessionID,
		SessionEnvKey:    CanonicalSessionEnv,
		Intent:           intent,
		ParentSessionID:  req.ParentSessionID,
		Definition:       req.Definition,
		Actor:            req.Actor,
		Runtime:          req.Runtime,
		ProviderMappings: append([]ProviderSessionMapping(nil), req.ProviderMappings...),
		LegacySessionIDs: append([]string(nil), req.LegacySessionIDs...),
		WorkRefs:         append([]WorkRef(nil), req.WorkRefs...),
		TraceRefs:        append([]TraceRef(nil), req.TraceRefs...),
		Publication:      req.Publication.OrDefault(),
		CreatedAt:        now,
	}
	if out.ParentSessionID == "" && intent == SessionBootstrapFork && req.Previous != nil {
		out.ParentSessionID = req.Previous.SessionID
	}
	if err := out.Validate(); err != nil {
		return SessionBootstrap{}, err
	}
	return out, nil
}

func resolveSessionID(req SessionBootstrapRequest, intent SessionBootstrapIntent) (string, error) {
	switch intent {
	case SessionBootstrapPreassigned:
		if err := ValidateSessionID(req.SessionID); err != nil {
			return "", err
		}
		return req.SessionID, nil
	case SessionBootstrapResume, SessionBootstrapCompact:
		if req.Previous == nil {
			return "", ErrBootstrapMissingPrevious
		}
		if err := req.Previous.Validate(); err != nil {
			return "", err
		}
		if req.SessionID != "" && req.SessionID != req.Previous.SessionID {
			return "", ErrBootstrapSessionCollision
		}
		return req.Previous.SessionID, nil
	case SessionBootstrapFresh, SessionBootstrapFork:
		if req.SessionID != "" {
			return "", ErrBootstrapUnexpectedSessionID
		}
		mint := req.Mint
		if mint == nil {
			mint = MintSessionID
		}
		var last string
		for i := 0; i < 4; i++ {
			candidate, err := mint()
			if err != nil {
				return "", err
			}
			last = candidate
			if err := ValidateSessionID(candidate); err != nil {
				return "", err
			}
			if req.Previous != nil && candidate == req.Previous.SessionID {
				continue
			}
			if req.ParentSessionID != "" && candidate == req.ParentSessionID {
				continue
			}
			return candidate, nil
		}
		_ = last
		return "", ErrBootstrapSessionCollision
	default:
		return "", ErrBootstrapUnknownIntent
	}
}

// MintSessionID returns a provider-neutral session ID suitable for the SESSION
// key. It intentionally does not encode host, path, provider, or user data.
func MintSessionID() (string, error) {
	return mintSessionIDFrom(rand.Reader)
}

func mintSessionIDFrom(r io.Reader) (string, error) {
	var raw [16]byte
	if _, err := io.ReadFull(r, raw[:]); err != nil {
		return "", err
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
	return "session-" + strings.ToLower(enc), nil
}

// ValidateSessionID rejects empty, path-like, renderer-artifact, and whitespace
// values. It permits existing legacy IDs so long as they are safe opaque tokens.
func ValidateSessionID(id string) error {
	return validateBootstrapSegment(id, true)
}

func validateBootstrapSegment(value string, required bool) error {
	if value == "" {
		if required {
			return ErrBootstrapMissingIdentity
		}
		return nil
	}
	if value == "." || value == ".." || value == "current" || value == "latest" {
		return ErrBootstrapUnsafeIdentity
	}
	if strings.ContainsAny(value, `/\\`) {
		return ErrBootstrapUnsafeIdentity
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return ErrBootstrapUnsafeIdentity
		}
	}
	return nil
}

// DeliveryCapability is provider/runtime vocabulary for what a live adapter can
// honestly do with a delivery obligation.
type DeliveryCapability string

const (
	DeliveryCapabilityPullOnly           DeliveryCapability = "pull-only"
	DeliveryCapabilityNextTurn           DeliveryCapability = "next-turn"
	DeliveryCapabilityBetweenToolCall    DeliveryCapability = "between-tool-call"
	DeliveryCapabilityInterrupt          DeliveryCapability = "interrupt"
	DeliveryCapabilityCancelTurn         DeliveryCapability = "cancel-turn"
	DeliveryCapabilityLifecycleStop      DeliveryCapability = "lifecycle-stop"
	DeliveryCapabilityDurableHostHandoff DeliveryCapability = "durable-host-handoff"
)

func (c DeliveryCapability) Valid() bool {
	switch c {
	case DeliveryCapabilityPullOnly, DeliveryCapabilityNextTurn, DeliveryCapabilityBetweenToolCall,
		DeliveryCapabilityInterrupt, DeliveryCapabilityCancelTurn, DeliveryCapabilityLifecycleStop,
		DeliveryCapabilityDurableHostHandoff:
		return true
	default:
		return false
	}
}

// DeliveryObservationStage names observable delivery progress. None of these
// stages asserts task success or model understanding.
type DeliveryObservationStage string

const (
	DeliveryObservedPersisted     DeliveryObservationStage = "persisted"
	DeliveryObservedLeaseAcquired DeliveryObservationStage = "lease-acquired"
	DeliveryObservedHostAccepted  DeliveryObservationStage = "host-accepted"
	DeliveryObservedTurnSubmitted DeliveryObservationStage = "turn-submitted"
	DeliveryObservedConsumed      DeliveryObservationStage = "consumed"
	DeliveryObservedFailed        DeliveryObservationStage = "failed"
	DeliveryObservedDeadLettered  DeliveryObservationStage = "dead-lettered"
	DeliveryObservedCanceled      DeliveryObservationStage = "canceled"
)

func (s DeliveryObservationStage) Valid() bool {
	switch s {
	case DeliveryObservedPersisted, DeliveryObservedLeaseAcquired, DeliveryObservedHostAccepted,
		DeliveryObservedTurnSubmitted, DeliveryObservedConsumed, DeliveryObservedFailed,
		DeliveryObservedDeadLettered, DeliveryObservedCanceled:
		return true
	default:
		return false
	}
}

// DeliveryObservation is a content-free bridge from runtime/provider adapters
// back to a reliable delivery store.
type DeliveryObservation struct {
	MessageID         string                   `yaml:"message_id" json:"message_id"`
	DeliveryID        string                   `yaml:"delivery_id" json:"delivery_id"`
	AttemptID         string                   `yaml:"attempt_id,omitempty" json:"attempt_id,omitempty"`
	Stage             DeliveryObservationStage `yaml:"stage" json:"stage"`
	SessionID         string                   `yaml:"session_id" json:"session_id"`
	ActorID           string                   `yaml:"actor_id,omitempty" json:"actor_id,omitempty"`
	RuntimeBindingID  string                   `yaml:"runtime_binding_id,omitempty" json:"runtime_binding_id,omitempty"`
	BindingGeneration int64                    `yaml:"binding_generation,omitempty" json:"binding_generation,omitempty"`
	Provider          string                   `yaml:"provider,omitempty" json:"provider,omitempty"`
	NativeSessionID   string                   `yaml:"native_session_id,omitempty" json:"native_session_id,omitempty"`
	ObservedAt        time.Time                `yaml:"observed_at" json:"observed_at"`
}

func (o DeliveryObservation) Validate() error {
	for label, value := range map[string]string{
		"message id":  o.MessageID,
		"delivery id": o.DeliveryID,
	} {
		if err := validateBootstrapSegment(value, true); err != nil {
			return fmt.Errorf("%w: %s", err, label)
		}
	}
	for label, value := range map[string]string{
		"attempt id":         o.AttemptID,
		"actor id":           o.ActorID,
		"runtime binding id": o.RuntimeBindingID,
		"provider":           o.Provider,
		"native session id":  o.NativeSessionID,
	} {
		if value != "" {
			if err := validateBootstrapSegment(value, false); err != nil {
				return fmt.Errorf("%w: %s", err, label)
			}
		}
	}
	if !o.Stage.Valid() {
		return ErrDeliveryObservationUnknownStage
	}
	if err := ValidateSessionID(o.SessionID); err != nil {
		return err
	}
	if o.BindingGeneration < 0 {
		return ErrBootstrapInvalidGeneration
	}
	if o.ObservedAt.IsZero() {
		return ErrDeliveryObservationMissingObservedAt
	}
	return nil
}
