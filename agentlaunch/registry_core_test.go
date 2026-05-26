package agentlaunch

import (
	"errors"
	"testing"
)

// fileBackedRegistrar returns a register-mode envelope registrar
// descriptor that satisfies RegistryRegistrar.Validate.
func fileBackedRegistrar() RegistryRegistrar {
	return RegistryRegistrar{Mode: RegistrarModeFileBacked, FileRoot: "/catalog"}
}

// testRecord builds a structurally-valid RegistrationRecord for the given
// facets so tests can populate the store without repeating boilerplate.
func testRecord(kind RegistryKind, name, owner, namespace, iface string, labels map[string]string) RegistrationRecord {
	return RegistrationRecord{
		Meta: RegistryContractMeta{
			Ref: RegistryObjectRef{
				Kind:      kind,
				Name:      name,
				Owner:     owner,
				Namespace: namespace,
			},
			SchemaVersion: AgentSourceSchemaVersionV1,
			Interface:     iface,
			Labels:        labels,
		},
		Source: RegistrationSource{FilePath: "/catalog/" + name + ".yaml"},
	}
}

// registerEnvelope wraps a record in a register envelope.
func registerEnvelope(rec RegistrationRecord, upsert bool) RegistryEnvelope {
	return RegistryEnvelope{
		Version:    "v1alpha1",
		Resolution: RegistryResolutionLocalFirst,
		Operation:  RegistryOperationRegister,
		Registrar:  fileBackedRegistrar(),
		Register:   &RegisterPayload{Record: rec, Upsert: upsert},
	}
}

// deregisterEnvelope wraps a ref in a deregister envelope.
func deregisterEnvelope(ref RegistryObjectRef) RegistryEnvelope {
	return RegistryEnvelope{
		Version:    "v1alpha1",
		Resolution: RegistryResolutionLocalFirst,
		Operation:  RegistryOperationDeregister,
		Registrar:  fileBackedRegistrar(),
		Deregister: &DeregisterPayload{Ref: ref},
	}
}

// queryEnvelope wraps a QueryPayload in a query envelope.
func queryEnvelope(q QueryPayload) RegistryEnvelope {
	return RegistryEnvelope{
		Version:    "v1alpha1",
		Resolution: RegistryResolutionLocalFirst,
		Operation:  RegistryOperationQuery,
		Registrar:  fileBackedRegistrar(),
		Query:      &q,
	}
}

func TestRegistrarRegisterQueryDeregisterHappyPath(t *testing.T) {
	r := NewInMemoryRegistrar()
	rec := testRecord(RegistryKindAgentSource, "nanite.backend", "nanite", "prod", AgentSourceInterfaceV1, nil)

	regResp, err := r.Handle(registerEnvelope(rec, false))
	if err != nil {
		t.Fatalf("register: unexpected error %v", err)
	}
	if regResp.Operation != RegistryOperationRegister || regResp.Replaced {
		t.Fatalf("register: got %+v, want register/!replaced", regResp)
	}
	if regResp.Ref == nil || *regResp.Ref != rec.Meta.Ref {
		t.Fatalf("register: ref = %+v, want %+v", regResp.Ref, rec.Meta.Ref)
	}
	if r.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", r.Len())
	}

	queryResp, err := r.Handle(queryEnvelope(QueryPayload{}))
	if err != nil {
		t.Fatalf("query: unexpected error %v", err)
	}
	if len(queryResp.Records) != 1 || queryResp.Records[0].Meta.Ref != rec.Meta.Ref {
		t.Fatalf("query: got %+v, want the one record", queryResp.Records)
	}

	deregResp, err := r.Handle(deregisterEnvelope(rec.Meta.Ref))
	if err != nil {
		t.Fatalf("deregister: unexpected error %v", err)
	}
	if deregResp.Operation != RegistryOperationDeregister || deregResp.Ref == nil {
		t.Fatalf("deregister: got %+v", deregResp)
	}
	if r.Len() != 0 {
		t.Fatalf("Len() after deregister = %d, want 0", r.Len())
	}

	// Query after deregister returns empty, not an error.
	after, err := r.Handle(queryEnvelope(QueryPayload{}))
	if err != nil {
		t.Fatalf("query after deregister: unexpected error %v", err)
	}
	if len(after.Records) != 0 {
		t.Fatalf("query after deregister: got %d records, want 0", len(after.Records))
	}
}

func TestRegistrarUpsertVsDuplicateReject(t *testing.T) {
	r := NewInMemoryRegistrar()
	rec := testRecord(RegistryKindAgentSource, "nanite.backend", "nanite", "prod", AgentSourceInterfaceV1, nil)

	if _, err := r.Handle(registerEnvelope(rec, false)); err != nil {
		t.Fatalf("first register: unexpected error %v", err)
	}

	// Duplicate ref with Upsert=false is rejected.
	_, err := r.Handle(registerEnvelope(rec, false))
	if !errors.Is(err, ErrRegistryDuplicateObject) {
		t.Fatalf("duplicate register: err = %v, want ErrRegistryDuplicateObject", err)
	}
	if r.Len() != 1 {
		t.Fatalf("Len() after rejected duplicate = %d, want 1", r.Len())
	}

	// Same ref with Upsert=true replaces and reports Replaced.
	updated := rec
	updated.Source.FilePath = "/catalog/nanite.backend.v2.yaml"
	resp, err := r.Handle(registerEnvelope(updated, true))
	if err != nil {
		t.Fatalf("upsert register: unexpected error %v", err)
	}
	if !resp.Replaced {
		t.Fatalf("upsert register: Replaced = false, want true")
	}
	if r.Len() != 1 {
		t.Fatalf("Len() after upsert = %d, want 1", r.Len())
	}

	got, err := r.Handle(queryEnvelope(QueryPayload{}))
	if err != nil {
		t.Fatalf("query: unexpected error %v", err)
	}
	if got.Records[0].Source.FilePath != "/catalog/nanite.backend.v2.yaml" {
		t.Fatalf("upsert did not replace record: %+v", got.Records[0])
	}
}

func TestRegistrarDeregisterAbsentRefErrors(t *testing.T) {
	r := NewInMemoryRegistrar()
	absent := RegistryObjectRef{Kind: RegistryKindMCPServer, Name: "ghost"}
	_, err := r.Handle(deregisterEnvelope(absent))
	if !errors.Is(err, ErrRegistryObjectNotFound) {
		t.Fatalf("deregister absent: err = %v, want ErrRegistryObjectNotFound", err)
	}
}

func TestRegistrarQueryFacetFilters(t *testing.T) {
	r := NewInMemoryRegistrar()
	records := []RegistrationRecord{
		testRecord(RegistryKindAgentSource, "nanite.backend", "nanite", "prod", AgentSourceInterfaceV1, map[string]string{"tier": "gold"}),
		testRecord(RegistryKindAgentSource, "nanite.frontend", "nanite", "dev", AgentSourceInterfaceV1, map[string]string{"tier": "silver"}),
		testRecord(RegistryKindMCPServer, "torque-loopback", "hollis", "prod", MCPServerInterfaceV1, map[string]string{"tier": "gold"}),
		testRecord(RegistryKindSkillSource, "review", "nanite", "prod", SkillSourceInterfaceV1, nil),
	}
	for _, rec := range records {
		if _, err := r.Handle(registerEnvelope(rec, false)); err != nil {
			t.Fatalf("seed register %s: %v", rec.Meta.Ref.Name, err)
		}
	}

	cases := []struct {
		name  string
		query QueryPayload
		want  []string
	}{
		{
			name:  "empty filter matches all",
			query: QueryPayload{},
			want:  []string{"nanite.frontend", "nanite.backend", "torque-loopback", "review"},
		},
		{
			name:  "filter by single kind",
			query: QueryPayload{Kinds: []RegistryKind{RegistryKindAgentSource}},
			want:  []string{"nanite.frontend", "nanite.backend"},
		},
		{
			name:  "filter by multiple kinds",
			query: QueryPayload{Kinds: []RegistryKind{RegistryKindMCPServer, RegistryKindSkillSource}},
			want:  []string{"torque-loopback", "review"},
		},
		{
			name:  "filter by owner",
			query: QueryPayload{Owner: "hollis"},
			want:  []string{"torque-loopback"},
		},
		{
			name:  "filter by namespace",
			query: QueryPayload{Namespace: "dev"},
			want:  []string{"nanite.frontend"},
		},
		{
			name:  "filter by name",
			query: QueryPayload{Name: "review"},
			want:  []string{"review"},
		},
		{
			name:  "filter by interface",
			query: QueryPayload{Interface: MCPServerInterfaceV1},
			want:  []string{"torque-loopback"},
		},
		{
			name:  "filter by label",
			query: QueryPayload{Labels: map[string]string{"tier": "gold"}},
			want:  []string{"nanite.backend", "torque-loopback"},
		},
		{
			name:  "facets AND together",
			query: QueryPayload{Owner: "nanite", Namespace: "prod", Labels: map[string]string{"tier": "gold"}},
			want:  []string{"nanite.backend"},
		},
		{
			name:  "no match returns empty",
			query: QueryPayload{Owner: "nobody"},
			want:  []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := r.Handle(queryEnvelope(tc.query))
			if err != nil {
				t.Fatalf("query: unexpected error %v", err)
			}
			got := make([]string, len(resp.Records))
			for i, rec := range resp.Records {
				got[i] = rec.Meta.Ref.Name
			}
			if !sameSet(got, tc.want) {
				t.Fatalf("query result = %v, want set %v", got, tc.want)
			}
		})
	}
}

func TestRegistrarHealthReport(t *testing.T) {
	r := NewInMemoryRegistrar()
	rec := testRecord(RegistryKindAgentSource, "nanite.backend", "nanite", "prod", AgentSourceInterfaceV1, nil)
	if _, err := r.Handle(registerEnvelope(rec, false)); err != nil {
		t.Fatalf("register: %v", err)
	}

	env := RegistryEnvelope{
		Version:    "v1alpha1",
		Resolution: RegistryResolutionLocalFirst,
		Operation:  RegistryOperationHealth,
		Registrar:  fileBackedRegistrar(),
		Health:     &HealthPayload{},
	}
	resp, err := r.Handle(env)
	if err != nil {
		t.Fatalf("health: unexpected error %v", err)
	}
	if resp.Health == nil {
		t.Fatalf("health: nil HealthPayload")
	}
	if resp.Health.Status != HealthStatusOK {
		t.Fatalf("health status = %q, want ok", resp.Health.Status)
	}
	if !resp.Health.DirectoryReachable {
		t.Fatalf("health: DirectoryReachable = false, want true (local-first store)")
	}
	if resp.Health.Detail == "" {
		t.Fatalf("health: empty Detail")
	}
}

func TestRegistrarRejectsInvalidEnvelope(t *testing.T) {
	r := NewInMemoryRegistrar()
	cases := []struct {
		name string
		env  RegistryEnvelope
		want error
	}{
		{
			name: "missing version",
			env: RegistryEnvelope{
				Resolution: RegistryResolutionLocalFirst,
				Operation:  RegistryOperationQuery,
				Registrar:  fileBackedRegistrar(),
				Query:      &QueryPayload{},
			},
			want: ErrRegistryMissingSchemaVersion,
		},
		{
			name: "register missing payload",
			env: RegistryEnvelope{
				Version:    "v1alpha1",
				Resolution: RegistryResolutionLocalFirst,
				Operation:  RegistryOperationRegister,
				Registrar:  fileBackedRegistrar(),
			},
			want: ErrRegistryMissingPayload,
		},
		{
			name: "register record missing local file ref",
			env: registerEnvelope(RegistrationRecord{
				Meta: RegistryContractMeta{
					Ref:           RegistryObjectRef{Kind: RegistryKindAgentSource, Name: "x"},
					SchemaVersion: AgentSourceSchemaVersionV1,
					Interface:     AgentSourceInterfaceV1,
				},
			}, false),
			want: ErrRegistryMissingLocalRef,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Handle(tc.env)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Handle() err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRegistrarRecordValidatorSeam(t *testing.T) {
	sentinel := errors.New("test: rejected by validator")
	r := NewInMemoryRegistrar(WithRecordValidator(func(rec RegistrationRecord) error {
		if rec.Meta.Ref.Name == "blocked" {
			return sentinel
		}
		return nil
	}))

	blocked := testRecord(RegistryKindAgentSource, "blocked", "", "", AgentSourceInterfaceV1, nil)
	if _, err := r.Handle(registerEnvelope(blocked, false)); !errors.Is(err, sentinel) {
		t.Fatalf("validator seam: err = %v, want sentinel", err)
	}
	if r.Len() != 0 {
		t.Fatalf("Len() = %d, want 0 (validator should block store)", r.Len())
	}

	allowed := testRecord(RegistryKindAgentSource, "allowed", "", "", AgentSourceInterfaceV1, nil)
	if _, err := r.Handle(registerEnvelope(allowed, false)); err != nil {
		t.Fatalf("validator seam: allowed record err = %v", err)
	}
	if r.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", r.Len())
	}
}

// sameSet reports whether a and b contain the same elements regardless of
// order.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, s := range a {
		counts[s]++
	}
	for _, s := range b {
		counts[s]--
	}
	for _, c := range counts {
		if c != 0 {
			return false
		}
	}
	return true
}
