package agentlaunch

import (
	"errors"
	"testing"
)

// validBusTopicContract returns a structurally-valid BusTopicContract so
// tests can mutate a single facet per case.
func validBusTopicContract() BusTopicContract {
	return BusTopicContract{
		Meta: RegistryContractMeta{
			Ref: RegistryObjectRef{
				Kind:      RegistryKindBusTopic,
				Name:      "tether.session.state",
				Owner:     "tether",
				Namespace: "prod",
			},
			SchemaVersion: BusTopicSchemaVersionV1,
			Interface:     BusTopicInterfaceV1,
		},
		Topic:     "tether/session/state",
		Direction: BusTopicDirectionEmit,
		SchemaRef: &RegistryObjectRef{
			Kind: RegistryKindContractObject,
			Name: "session-state-schema",
		},
		Transport: "tether-bus",
	}
}

func TestBusTopicContractValidateHappyPath(t *testing.T) {
	cases := []struct {
		name      string
		direction BusTopicDirection
	}{
		{"emit", BusTopicDirectionEmit},
		{"subscribe", BusTopicDirectionSubscribe},
		{"bidirectional", BusTopicDirectionBidirectional},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contract := validBusTopicContract()
			contract.Direction = tc.direction
			if err := contract.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}

	// SchemaRef and Transport are optional: a minimal handle still validates.
	t.Run("minimal without optional fields", func(t *testing.T) {
		contract := validBusTopicContract()
		contract.SchemaRef = nil
		contract.Transport = ""
		if err := contract.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})
}

func TestBusTopicContractValidateRejections(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*BusTopicContract)
		wantErr error
	}{
		{
			name:    "bad kind",
			mutate:  func(c *BusTopicContract) { c.Meta.Ref.Kind = RegistryKindMCPServer },
			wantErr: ErrRegistryUnknownKind,
		},
		{
			name:    "bad schema version",
			mutate:  func(c *BusTopicContract) { c.Meta.SchemaVersion = "v2" },
			wantErr: ErrRegistryMissingSchemaVersion,
		},
		{
			name:    "missing schema version",
			mutate:  func(c *BusTopicContract) { c.Meta.SchemaVersion = "" },
			wantErr: ErrRegistryMissingSchemaVersion,
		},
		{
			name:    "bad interface",
			mutate:  func(c *BusTopicContract) { c.Meta.Interface = "wrong/v1" },
			wantErr: ErrRegistryMissingInterface,
		},
		{
			name:    "missing interface",
			mutate:  func(c *BusTopicContract) { c.Meta.Interface = "" },
			wantErr: ErrRegistryMissingInterface,
		},
		{
			name:    "missing name",
			mutate:  func(c *BusTopicContract) { c.Meta.Ref.Name = "" },
			wantErr: ErrRegistryMissingName,
		},
		{
			name:    "missing topic",
			mutate:  func(c *BusTopicContract) { c.Topic = "" },
			wantErr: ErrRegistryBusTopicMissingTopic,
		},
		{
			name:    "invalid direction",
			mutate:  func(c *BusTopicContract) { c.Direction = BusTopicDirection("publish") },
			wantErr: ErrRegistryBusTopicUnknownDirection,
		},
		{
			name:    "empty direction",
			mutate:  func(c *BusTopicContract) { c.Direction = "" },
			wantErr: ErrRegistryBusTopicUnknownDirection,
		},
		{
			name:    "schema ref wrong kind",
			mutate:  func(c *BusTopicContract) { c.SchemaRef.Kind = RegistryKindBusTopic },
			wantErr: ErrRegistryUnknownKind,
		},
		{
			name:    "schema ref missing name",
			mutate:  func(c *BusTopicContract) { c.SchemaRef.Name = "" },
			wantErr: ErrRegistryMissingName,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contract := validBusTopicContract()
			tc.mutate(&contract)
			err := contract.Validate()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestBusTopicDirectionValid(t *testing.T) {
	cases := []struct {
		direction BusTopicDirection
		want      bool
	}{
		{BusTopicDirectionEmit, true},
		{BusTopicDirectionSubscribe, true},
		{BusTopicDirectionBidirectional, true},
		{BusTopicDirection(""), false},
		{BusTopicDirection("publish"), false},
	}
	for _, tc := range cases {
		if got := tc.direction.Valid(); got != tc.want {
			t.Fatalf("BusTopicDirection(%q).Valid() = %v, want %v", tc.direction, got, tc.want)
		}
	}
}

func TestRegistryKindBusTopicAcceptedByEnvelope(t *testing.T) {
	if !RegistryKindBusTopic.Valid() {
		t.Fatalf("RegistryKindBusTopic.Valid() = false, want true")
	}
	// A query envelope filtering on the new kind must validate.
	env := queryEnvelope(QueryPayload{Kinds: []RegistryKind{RegistryKindBusTopic}})
	if err := env.Validate(); err != nil {
		t.Fatalf("query envelope with bus-topic kind: Validate() error = %v", err)
	}
}

// TestBusTopicRegisterThenQuery exercises the S3.5 acceptance criterion
// end to end: a bus-topic registration record is registered through the
// S3.1 InMemoryRegistrar.Handle and then discovered by a query filtered
// to the new kind.
func TestBusTopicRegisterThenQuery(t *testing.T) {
	r := NewInMemoryRegistrar()

	busRec := RegistrationRecord{
		Meta: RegistryContractMeta{
			Ref: RegistryObjectRef{
				Kind:      RegistryKindBusTopic,
				Name:      "tether.session.state",
				Owner:     "tether",
				Namespace: "prod",
			},
			SchemaVersion: BusTopicSchemaVersionV1,
			Interface:     BusTopicInterfaceV1,
		},
		Source: RegistrationSource{FilePath: "/catalog/tether.session.state.yaml"},
	}
	// A non-bus record so the kind filter has something to exclude.
	otherRec := testRecord(RegistryKindMCPServer, "torque-loopback", "hollis", "prod", MCPServerInterfaceV1, nil)

	for _, rec := range []RegistrationRecord{busRec, otherRec} {
		if _, err := r.Handle(registerEnvelope(rec, false)); err != nil {
			t.Fatalf("register %s: unexpected error %v", rec.Meta.Ref.Name, err)
		}
	}

	resp, err := r.Handle(queryEnvelope(QueryPayload{Kinds: []RegistryKind{RegistryKindBusTopic}}))
	if err != nil {
		t.Fatalf("query: unexpected error %v", err)
	}
	if len(resp.Records) != 1 {
		t.Fatalf("query bus-topic: got %d records, want 1", len(resp.Records))
	}
	if resp.Records[0].Meta.Ref != busRec.Meta.Ref {
		t.Fatalf("query bus-topic: ref = %+v, want %+v", resp.Records[0].Meta.Ref, busRec.Meta.Ref)
	}
}

// TestBusTopicRegistersThroughValidatorSeam confirms a BusTopicContract's
// own Validate can be plugged into the S3.1 record-validator seam so a
// bus-topic registration is contract-validated before it is stored.
func TestBusTopicRegistersThroughValidatorSeam(t *testing.T) {
	contract := validBusTopicContract()
	r := NewInMemoryRegistrar(WithRecordValidator(func(rec RegistrationRecord) error {
		if rec.Meta.Ref.Kind != RegistryKindBusTopic {
			return nil
		}
		// Re-derive the contract handle from the record meta and validate.
		bt := BusTopicContract{Meta: rec.Meta, Topic: contract.Topic, Direction: contract.Direction}
		return bt.Validate()
	}))

	rec := RegistrationRecord{
		Meta:   contract.Meta,
		Source: RegistrationSource{FilePath: "/catalog/tether.session.state.yaml"},
	}
	if _, err := r.Handle(registerEnvelope(rec, false)); err != nil {
		t.Fatalf("register validated bus-topic: unexpected error %v", err)
	}
	if r.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", r.Len())
	}
}
