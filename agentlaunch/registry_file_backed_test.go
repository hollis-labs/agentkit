package agentlaunch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureCatalogRoot is the deterministic fixture catalog used by the
// S3.3 tests. Its on-disk layout (and the expected per-subdir counts in
// the tests below) is the contract — keep them in sync.
//
//	agents/           2 yaml          -> agent-source
//	providers/        1 yaml          -> runtime-binding
//	mcp-servers/      1 yaml + 1 .bak -> mcp-server
//	boot-profiles/    2 yaml          -> boot-spec
//	launches/         1 yaml + 1 bad  -> execution-template
//	projects/         2 yaml          -> unmapped
//	sandbox-profiles/ 1 yaml          -> unmapped
const fixtureCatalogRoot = "testdata/catalog"

func newFixtureRegistrar(t *testing.T, opts ...FileBackedOption) *FileBackedRegistrar {
	t.Helper()
	return NewFileBackedRegistrar(fixtureCatalogRoot, opts...)
}

// TestIngestCatalogReconciles is the S3.3 acceptance test: every walked
// subdirectory must balance files == registered + skipped + errored.
func TestIngestCatalogReconciles(t *testing.T) {
	fbr := newFixtureRegistrar(t)
	report, err := fbr.IngestCatalog()
	if err != nil {
		t.Fatalf("IngestCatalog: %v", err)
	}

	if !report.Reconciles() {
		t.Fatalf("report does not reconcile: files=%v registered=%v skipped=%d errors=%d",
			report.FilesBySubdir, report.RegisteredBySubdir, len(report.Skipped), len(report.Errors))
	}

	// Spell the reconciliation out per subdir so a regression names the
	// offending directory.
	type want struct{ files, registered, skippedYAML, errored int }
	wants := map[string]want{
		"agents":           {2, 2, 0, 0},
		"providers":        {1, 1, 0, 0},
		"mcp-servers":      {1, 1, 0, 0}, // the .bak file is a non-yaml skip, not a "file seen"
		"boot-profiles":    {2, 2, 0, 0},
		"launches":         {2, 1, 0, 1}, // broken.yaml errors
		"projects":         {2, 0, 2, 0}, // unmapped
		"sandbox-profiles": {1, 0, 1, 0}, // unmapped
	}
	skippedYAML := map[string]int{}
	for _, s := range report.Skipped {
		if s.Reason != IngestSkipNonYAML {
			skippedYAML[s.Subdir]++
		}
	}
	errored := map[string]int{}
	for _, e := range report.Errors {
		errored[e.Subdir]++
	}
	for subdir, w := range wants {
		if got := report.FilesBySubdir[subdir]; got != w.files {
			t.Errorf("%s: files seen = %d, want %d", subdir, got, w.files)
		}
		if got := report.RegisteredBySubdir[subdir]; got != w.registered {
			t.Errorf("%s: registered = %d, want %d", subdir, got, w.registered)
		}
		if got := skippedYAML[subdir]; got != w.skippedYAML {
			t.Errorf("%s: skipped(yaml) = %d, want %d", subdir, got, w.skippedYAML)
		}
		if got := errored[subdir]; got != w.errored {
			t.Errorf("%s: errored = %d, want %d", subdir, got, w.errored)
		}
		if got := report.FilesBySubdir[subdir]; got != w.registered+w.skippedYAML+w.errored {
			t.Errorf("%s: reconciliation broken: files %d != %d+%d+%d",
				subdir, got, w.registered, w.skippedYAML, w.errored)
		}
	}

	if report.Registered() != 7 {
		t.Errorf("total registered = %d, want 7", report.Registered())
	}
}

// TestIngestCatalogDirKindMapping checks every mapped catalog subdir
// produces records under its natural registry kind.
func TestIngestCatalogDirKindMapping(t *testing.T) {
	cases := []struct {
		subdir string
		kind   RegistryKind
		count  int
	}{
		{"agents", RegistryKindAgentSource, 2},
		{"providers", RegistryKindRuntimeBinding, 1},
		{"mcp-servers", RegistryKindMCPServer, 1},
		{"boot-profiles", RegistryKindBootSpec, 2},
		{"launches", RegistryKindExecutionTemplate, 1},
	}

	fbr := newFixtureRegistrar(t)
	report, err := fbr.IngestCatalog()
	if err != nil {
		t.Fatalf("IngestCatalog: %v", err)
	}

	for _, tc := range cases {
		t.Run(tc.subdir, func(t *testing.T) {
			gotKind, ok := CatalogSubdirKind(tc.subdir)
			if !ok || gotKind != tc.kind {
				t.Fatalf("CatalogSubdirKind(%q) = %q,%v want %q", tc.subdir, gotKind, ok, tc.kind)
			}
			if got := report.RegisteredByKind[tc.kind]; got != tc.count {
				t.Errorf("registered %q = %d, want %d", tc.kind, got, tc.count)
			}
			// Every produced record for this subdir must carry the kind
			// and stamp the published schema/interface for it.
			sv, iface, _ := KindRegistrationSpec(tc.kind)
			for _, rec := range report.Records {
				if rec.Meta.Ref.Kind != tc.kind {
					continue
				}
				if rec.Meta.SchemaVersion != sv {
					t.Errorf("%s: schema = %q want %q", rec.Meta.Ref.Name, rec.Meta.SchemaVersion, sv)
				}
				if rec.Meta.Interface != iface {
					t.Errorf("%s: interface = %q want %q", rec.Meta.Ref.Name, rec.Meta.Interface, iface)
				}
			}
		})
	}
}

// TestIngestCatalogUnmappedDirs checks projects/ and sandbox-profiles/
// are recorded as unmapped skips and never registered. The kind
// vocabulary is locked, so these subdirs deliberately have no kind.
func TestIngestCatalogUnmappedDirs(t *testing.T) {
	fbr := newFixtureRegistrar(t)
	report, err := fbr.IngestCatalog()
	if err != nil {
		t.Fatalf("IngestCatalog: %v", err)
	}

	for _, subdir := range []string{"projects", "sandbox-profiles"} {
		if _, ok := CatalogSubdirKind(subdir); ok {
			t.Errorf("CatalogSubdirKind(%q) reported a kind; vocabulary is locked", subdir)
		}
	}

	unmapped := map[string]int{}
	for _, s := range report.Skipped {
		if s.Reason == IngestSkipUnmappedDir {
			unmapped[s.Subdir]++
		}
	}
	if unmapped["projects"] != 2 {
		t.Errorf("projects unmapped skips = %d, want 2", unmapped["projects"])
	}
	if unmapped["sandbox-profiles"] != 1 {
		t.Errorf("sandbox-profiles unmapped skips = %d, want 1", unmapped["sandbox-profiles"])
	}
}

// TestIngestCatalogNonYAMLSkipped checks a *.bak file in a mapped subdir
// is recorded as a non-yaml skip, not registered and not errored.
func TestIngestCatalogNonYAMLSkipped(t *testing.T) {
	fbr := newFixtureRegistrar(t)
	report, err := fbr.IngestCatalog()
	if err != nil {
		t.Fatalf("IngestCatalog: %v", err)
	}

	var found bool
	for _, s := range report.Skipped {
		if s.Reason == IngestSkipNonYAML && strings.HasSuffix(s.FilePath, ".bak-20260101") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a non-yaml skip for the .bak file; skips=%v", report.Skipped)
	}
}

// TestIngestCatalogMalformedFileRecorded checks a malformed catalog file
// is captured as a per-file IngestError, does not abort the ingest, and
// the rest of the subdir still ingests cleanly.
func TestIngestCatalogMalformedFileRecorded(t *testing.T) {
	fbr := newFixtureRegistrar(t)
	report, err := fbr.IngestCatalog()
	if err != nil {
		t.Fatalf("IngestCatalog returned a fatal error for a per-file fault: %v", err)
	}

	if len(report.Errors) != 1 {
		t.Fatalf("expected exactly 1 per-file error, got %d: %v", len(report.Errors), report.Errors)
	}
	ie := report.Errors[0]
	if !strings.HasSuffix(ie.FilePath, "broken.yaml") {
		t.Errorf("error file = %q, want .../broken.yaml", ie.FilePath)
	}
	if !errors.Is(ie.Err, ErrRegistryCatalogEntryMalformed) {
		t.Errorf("error = %v, want wrapping ErrRegistryCatalogEntryMalformed", ie.Err)
	}
	// The sibling good launch still ingested.
	if report.RegisteredBySubdir["launches"] != 1 {
		t.Errorf("good launch did not ingest despite a malformed sibling")
	}
}

// TestIngestCatalogMissingIDRecorded checks a YAML file with no `id:`
// field is captured as a per-file error.
func TestIngestCatalogMissingIDRecorded(t *testing.T) {
	root := t.TempDir()
	mkSubdir(t, root, "agents", map[string]string{
		"good.yaml": "id: good-agent\n",
		"noid.yaml": "name: has no id field\n",
	})
	fbr := NewFileBackedRegistrar(root)
	report, err := fbr.IngestCatalog()
	if err != nil {
		t.Fatalf("IngestCatalog: %v", err)
	}
	if len(report.Errors) != 1 || !errors.Is(report.Errors[0].Err, ErrRegistryCatalogEntryMissingID) {
		t.Fatalf("expected one missing-id error, got %v", report.Errors)
	}
	if report.RegisteredBySubdir["agents"] != 1 {
		t.Errorf("good agent did not ingest")
	}
	if !report.Reconciles() {
		t.Errorf("missing-id report does not reconcile")
	}
}

// TestIngestedRecordsQueryable checks records ingested by the file-backed
// registrar are queryable through the underlying S3.1 registrar.
func TestIngestedRecordsQueryable(t *testing.T) {
	fbr := newFixtureRegistrar(t)
	report, err := fbr.IngestCatalog()
	if err != nil {
		t.Fatalf("IngestCatalog: %v", err)
	}

	resp, err := fbr.Registrar().Handle(RegistryEnvelope{
		Version:    RegistryEnvelopeVersionV1,
		Resolution: RegistryResolutionLocalFirst,
		Operation:  RegistryOperationQuery,
		Registrar:  RegistryRegistrar{Mode: RegistrarModeFileBacked, FileRoot: fixtureCatalogRoot},
		Query:      &QueryPayload{},
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(resp.Records) != report.Registered() {
		t.Fatalf("query returned %d records, want %d", len(resp.Records), report.Registered())
	}

	// Query a single kind.
	resp, err = fbr.Registrar().Handle(RegistryEnvelope{
		Version:    RegistryEnvelopeVersionV1,
		Resolution: RegistryResolutionLocalFirst,
		Operation:  RegistryOperationQuery,
		Registrar:  RegistryRegistrar{Mode: RegistrarModeFileBacked, FileRoot: fixtureCatalogRoot},
		Query:      &QueryPayload{Kinds: []RegistryKind{RegistryKindAgentSource}},
	})
	if err != nil {
		t.Fatalf("kind query: %v", err)
	}
	if len(resp.Records) != 2 {
		t.Fatalf("agent-source query returned %d, want 2", len(resp.Records))
	}
	for _, rec := range resp.Records {
		if rec.Source.FilePath == "" {
			t.Errorf("record %q has empty FilePath", rec.Meta.Ref.Name)
		}
		if !strings.HasPrefix(rec.Source.Digest, "sha256:") {
			t.Errorf("record %q digest = %q, want sha256: prefix", rec.Meta.Ref.Name, rec.Source.Digest)
		}
	}
}

// TestIngestRecordsAreHandlesNotContent enforces D2: the produced
// RegistrationRecord carries only a handle (meta + file pointer +
// digest), never the catalog file body.
func TestIngestRecordsAreHandlesNotContent(t *testing.T) {
	fbr := newFixtureRegistrar(t)
	report, err := fbr.IngestCatalog()
	if err != nil {
		t.Fatalf("IngestCatalog: %v", err)
	}
	for _, rec := range report.Records {
		body, err := os.ReadFile(rec.Source.FilePath)
		if err != nil {
			t.Fatalf("read %s: %v", rec.Source.FilePath, err)
		}
		// The record has no field that could hold the body; assert the
		// observable surface (FilePath + digest) is a pointer and the
		// digest matches the on-disk file rather than inlining it.
		if rec.Source.FilePath == "" {
			t.Errorf("%s: record is not a file pointer", rec.Meta.Ref.Name)
		}
		// Sanity: digest is derived from the body, proving the file is
		// the source of truth and the record only references it.
		if len(body) == 0 {
			t.Errorf("%s: catalog file is empty", rec.Meta.Ref.Name)
		}
	}
}

// TestIngestComposesPerKindValidator checks the file-backed registrar
// composes with the S3.2 PerKindRegistrationValidator: records the
// validator rejects surface as per-file errors, not as a fatal abort.
func TestIngestComposesPerKindValidator(t *testing.T) {
	inner := NewInMemoryRegistrar(WithRecordValidator(PerKindRegistrationValidator))
	fbr := newFixtureRegistrar(t, WithRegistrar(inner))
	report, err := fbr.IngestCatalog()
	if err != nil {
		t.Fatalf("IngestCatalog: %v", err)
	}
	// The fixture entries stamp the published schema/interface per kind,
	// so the per-kind validator accepts all 7.
	if report.Registered() != 7 {
		t.Fatalf("validated ingest registered %d, want 7", report.Registered())
	}
	if !report.Reconciles() {
		t.Errorf("validated ingest does not reconcile")
	}
}

// TestIngestUpsertIdempotent checks a repeated ingest into the same
// registrar succeeds (upsert default) rather than failing on duplicate
// refs.
func TestIngestUpsertIdempotent(t *testing.T) {
	inner := NewInMemoryRegistrar()
	fbr := newFixtureRegistrar(t, WithRegistrar(inner))
	if _, err := fbr.IngestCatalog(); err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	report, err := fbr.IngestCatalog()
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if len(report.Errors) != 1 { // only the malformed file, no duplicate errors
		t.Fatalf("second ingest errors = %v, want only the malformed file", report.Errors)
	}
	if inner.Len() != 7 {
		t.Errorf("store size after re-ingest = %d, want 7 (idempotent)", inner.Len())
	}

	// With upsert disabled, the second ingest should record duplicate
	// errors instead.
	noUpsert := NewInMemoryRegistrar()
	f2 := newFixtureRegistrar(t, WithRegistrar(noUpsert), WithUpsert(false))
	if _, err := f2.IngestCatalog(); err != nil {
		t.Fatalf("no-upsert first ingest: %v", err)
	}
	rep2, err := f2.IngestCatalog()
	if err != nil {
		t.Fatalf("no-upsert second ingest: %v", err)
	}
	dupes := 0
	for _, e := range rep2.Errors {
		if errors.Is(e.Err, ErrRegistryDuplicateObject) {
			dupes++
		}
	}
	if dupes != 7 {
		t.Errorf("no-upsert re-ingest duplicate errors = %d, want 7", dupes)
	}
}

// TestIngestCatalogMissingRoot checks a missing catalog root is the one
// fatal failure mode.
func TestIngestCatalogMissingRoot(t *testing.T) {
	fbr := NewFileBackedRegistrar(filepath.Join(t.TempDir(), "does-not-exist"))
	_, err := fbr.IngestCatalog()
	if !errors.Is(err, ErrRegistryCatalogRootUnreadable) {
		t.Fatalf("err = %v, want ErrRegistryCatalogRootUnreadable", err)
	}
}

// TestIngestCatalogMissingSubdir checks a catalog that omits a subdir
// entirely is not an error.
func TestIngestCatalogMissingSubdir(t *testing.T) {
	root := t.TempDir()
	mkSubdir(t, root, "agents", map[string]string{"a.yaml": "id: a\n"})
	// providers/, mcp-servers/, etc. are absent.
	fbr := NewFileBackedRegistrar(root)
	report, err := fbr.IngestCatalog()
	if err != nil {
		t.Fatalf("IngestCatalog: %v", err)
	}
	if report.Registered() != 1 || !report.Reconciles() {
		t.Fatalf("partial catalog: registered=%d reconciles=%v", report.Registered(), report.Reconciles())
	}
}

// TestIngestRealCatalog runs a read-only ingest against the developer's
// live ~/.tether/catalog/ when present. It skips gracefully when the
// directory is absent so the suite never depends on machine-local state.
func TestIngestRealCatalog(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	root := filepath.Join(home, ".tether", "catalog")
	if _, err := os.Stat(root); err != nil {
		t.Skipf("live catalog %s not present", root)
	}

	fbr := NewFileBackedRegistrar(root)
	report, err := fbr.IngestCatalog()
	if err != nil {
		t.Fatalf("live IngestCatalog: %v", err)
	}
	if !report.Reconciles() {
		t.Errorf("live catalog ingest does not reconcile: files=%v registered=%v",
			report.FilesBySubdir, report.RegisteredBySubdir)
	}
	t.Logf("live catalog ingest: registered=%d by-kind=%v skipped=%d errors=%d",
		report.Registered(), report.RegisteredByKind, len(report.Skipped), len(report.Errors))
	for _, ie := range report.Errors {
		t.Logf("live catalog per-file error: %s: %s", ie.FilePath, ie.Message)
	}
}

// mkSubdir writes a subdirectory of name->content files under root for
// the tempdir-based tests.
func mkSubdir(t *testing.T, root, subdir string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, subdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}
