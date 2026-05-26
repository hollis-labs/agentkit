package agentlaunch

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// registry_file_backed.go ships the S3.3 file-backed registrar: a
// permanent first-class registrar mode that walks a Tether-style catalog
// root and produces one RegistrationRecord per catalog entry, then loads
// those records into an S3.1 Registrar.
//
// Dual role — both first-class:
//
//   - Drop-a-file registration. Dropping a contract file into a mapped
//     catalog subdirectory is a supported, permanent way to register an
//     object. This is NOT migration scaffolding; per design decision D1
//     the file-backed registrar is a permanent first-class mode.
//
//   - Migration bridge. The same walk populates the new directory
//     registry from the existing ~/.tether/catalog/ files and is the
//     new-side input for the S4.5 parity harness.
//
// Design constraints honored here:
//
//   - D1 (local-first; file-backed registrar is a permanent first-class
//     mode). IngestCatalog is pure local filesystem I/O: os.ReadDir +
//     os.ReadFile only. There is no network path. The registrar it feeds
//     is the in-memory S3.1 core, which is itself offline.
//
//   - D2 (the directory holds resolver handles, NOT content). Each
//     RegistrationRecord this file produces carries a RegistrationSource
//     whose FilePath points at the catalog file (the file remains the
//     source of truth) plus a content digest. The file body is read ONLY
//     to extract the entry identity (its `id:` field) and compute the
//     digest; the body is never inlined or snapshotted into the
//     RegistrationRecord or the registrar store. The registry holds the
//     handle; the file holds the content.

// catalogSubdirKind maps one Tether catalog subdirectory name to the
// registry kind its entries register under.
//
// The five mapped subdirectories cover the kinds that have a natural
// home in the locked 8-kind vocabulary:
//
//	agents/        -> agent-source
//	providers/     -> runtime-binding
//	mcp-servers/   -> mcp-server
//	boot-profiles/ -> boot-spec
//	launches/      -> execution-template
//
// projects/ and sandbox-profiles/ deliberately have NO entry here. The
// registry kind vocabulary is locked (S1.3 plus S3.5's bus-topic was the
// final addition); inventing a "project" or "sandbox-profile" kind is
// out of bounds. Entries under an unmapped subdirectory are recorded as
// an "unmapped" outcome in the IngestReport (see IngestCatalog) rather
// than silently dropped, so entry-count reconciliation still holds.
var catalogSubdirKind = map[string]RegistryKind{
	"agents":        RegistryKindAgentSource,
	"providers":     RegistryKindRuntimeBinding,
	"mcp-servers":   RegistryKindMCPServer,
	"boot-profiles": RegistryKindBootSpec,
	"launches":      RegistryKindExecutionTemplate,
}

// unmappedCatalogSubdirs are the Tether catalog subdirectories that are
// recognised as catalog content but have no natural registry kind. They
// are walked so their entries can be counted and reported as unmapped;
// they are never registered.
var unmappedCatalogSubdirs = map[string]struct{}{
	"projects":         {},
	"sandbox-profiles": {},
}

// CatalogSubdirKind reports the registry kind a catalog subdirectory
// maps to. ok is false when the subdirectory has no mapped kind — either
// because it is a recognised-but-unmapped subdir (projects,
// sandbox-profiles) or because it is not a known catalog subdir at all.
//
// It is exported so callers can introspect the dir->kind mapping without
// running a full ingest.
func CatalogSubdirKind(subdir string) (kind RegistryKind, ok bool) {
	k, found := catalogSubdirKind[subdir]
	return k, found
}

// IngestSkipReason classifies why a catalog entry was not registered.
type IngestSkipReason string

const (
	// IngestSkipUnmappedDir marks an entry that lives under a catalog
	// subdirectory with no natural registry kind (projects,
	// sandbox-profiles). The kind vocabulary is locked, so these entries
	// are deliberately not registered.
	IngestSkipUnmappedDir IngestSkipReason = "unmapped-directory"

	// IngestSkipNonYAML marks a file that is not a *.yaml / *.yml
	// document (for example a *.bak-* backup file left in the catalog).
	IngestSkipNonYAML IngestSkipReason = "non-yaml-file"
)

// IngestSkip records one catalog entry that was intentionally not
// registered, with the reason and the file it came from.
type IngestSkip struct {
	FilePath string           `yaml:"file_path" json:"file_path"`
	Subdir   string           `yaml:"subdir" json:"subdir"`
	Reason   IngestSkipReason `yaml:"reason" json:"reason"`
	Detail   string           `yaml:"detail,omitempty" json:"detail,omitempty"`
}

// IngestError records one catalog file that could not be turned into a
// RegistrationRecord. A malformed file is captured here and does not
// abort the ingest.
type IngestError struct {
	FilePath string `yaml:"file_path" json:"file_path"`
	Subdir   string `yaml:"subdir" json:"subdir"`
	Err      error  `yaml:"-" json:"-"`
	Message  string `yaml:"message" json:"message"`
}

// IngestReport is the structured result of one IngestCatalog run. It is
// the reconciliation surface: for every catalog subdirectory walked, the
// number of files seen equals registered + skipped + errored for that
// subdirectory (see Reconciles).
type IngestReport struct {
	// Root is the catalog root directory that was walked.
	Root string `yaml:"root" json:"root"`

	// RegisteredByKind counts successfully-registered records per
	// registry kind.
	RegisteredByKind map[RegistryKind]int `yaml:"registered_by_kind" json:"registered_by_kind"`

	// FilesBySubdir counts every *.yaml / *.yml file seen per catalog
	// subdirectory, before mapping. Non-YAML files are not counted here;
	// they are reported as skips with reason non-yaml-file.
	FilesBySubdir map[string]int `yaml:"files_by_subdir" json:"files_by_subdir"`

	// RegisteredBySubdir counts successfully-registered records per
	// catalog subdirectory.
	RegisteredBySubdir map[string]int `yaml:"registered_by_subdir" json:"registered_by_subdir"`

	// Skipped lists every entry intentionally not registered.
	Skipped []IngestSkip `yaml:"skipped,omitempty" json:"skipped,omitempty"`

	// Errors lists every file that failed to ingest. A non-empty Errors
	// slice does NOT mean the ingest aborted — each error is per-file.
	Errors []IngestError `yaml:"errors,omitempty" json:"errors,omitempty"`

	// Records holds every RegistrationRecord produced by the walk, in the
	// order they were registered. The records carry handles only (D2).
	Records []RegistrationRecord `yaml:"records,omitempty" json:"records,omitempty"`
}

// Registered reports the total number of records registered across all
// kinds.
func (r IngestReport) Registered() int {
	n := 0
	for _, c := range r.RegisteredByKind {
		n += c
	}
	return n
}

// Reconciles reports whether every walked subdirectory balances:
// files seen == registered + skipped + errored. This is the S3.3
// acceptance check. A non-YAML file is counted as a skip but not as a
// "file seen", so non-YAML skips are excluded from both sides and the
// identity still holds over YAML entries.
func (r IngestReport) Reconciles() bool {
	skippedYAML := map[string]int{}
	errored := map[string]int{}
	for _, s := range r.Skipped {
		if s.Reason == IngestSkipNonYAML {
			continue
		}
		skippedYAML[s.Subdir]++
	}
	for _, e := range r.Errors {
		errored[e.Subdir]++
	}
	for subdir, files := range r.FilesBySubdir {
		if files != r.RegisteredBySubdir[subdir]+skippedYAML[subdir]+errored[subdir] {
			return false
		}
	}
	return true
}

// FileBackedRegistrar walks a catalog root and registers one record per
// catalog entry into an underlying S3.1 Registrar. It is a permanent
// first-class registrar mode (D1), not migration scaffolding.
type FileBackedRegistrar struct {
	root      string
	registrar Registrar
	upsert    bool
}

// FileBackedOption configures a FileBackedRegistrar at construction time.
type FileBackedOption func(*FileBackedRegistrar)

// WithRegistrar installs the underlying Registrar the file-backed
// registrar loads records into. When omitted, a fresh InMemoryRegistrar
// is constructed. Supplying one lets callers compose S3.2 per-kind
// validation, for example:
//
//	inner := NewInMemoryRegistrar(WithRecordValidator(PerKindRegistrationValidator))
//	fbr := NewFileBackedRegistrar(root, WithRegistrar(inner))
func WithRegistrar(r Registrar) FileBackedOption {
	return func(f *FileBackedRegistrar) {
		if r != nil {
			f.registrar = r
		}
	}
}

// WithUpsert controls whether catalog entries register with upsert
// semantics. With upsert enabled a repeated ingest into the same
// registrar refreshes records instead of failing on the duplicate ref.
// It defaults to true because re-walking a catalog is an expected,
// idempotent operation for a permanent file-backed registrar.
func WithUpsert(upsert bool) FileBackedOption {
	return func(f *FileBackedRegistrar) {
		f.upsert = upsert
	}
}

// NewFileBackedRegistrar constructs a file-backed registrar anchored at
// catalog root. It performs no I/O at construction time and has no
// network dependency (D1). Call IngestCatalog to walk the root.
func NewFileBackedRegistrar(root string, opts ...FileBackedOption) *FileBackedRegistrar {
	f := &FileBackedRegistrar{
		root:   root,
		upsert: true,
	}
	for _, opt := range opts {
		opt(f)
	}
	if f.registrar == nil {
		f.registrar = NewInMemoryRegistrar()
	}
	return f
}

// Registrar returns the underlying S3.1 Registrar the file-backed
// registrar feeds. Records ingested by IngestCatalog are queryable
// through it via the standard RegistryEnvelope query operation.
func (f *FileBackedRegistrar) Registrar() Registrar {
	return f.registrar
}

// Root returns the catalog root directory the registrar is anchored at.
func (f *FileBackedRegistrar) Root() string {
	return f.root
}

// IngestCatalog walks the catalog root, builds one RegistrationRecord per
// mapped catalog entry, registers it through the underlying Registrar,
// and returns a structured IngestReport.
//
// Behavior contract:
//
//   - Every subdirectory listed in catalogSubdirKind plus every
//     recognised-but-unmapped subdirectory is walked non-recursively.
//   - Files under a mapped subdir become RegistrationRecords (D2: handle
//     only — FilePath + digest, never the body).
//   - Files under an unmapped subdir are recorded as IngestSkip with
//     reason unmapped-directory.
//   - A non-YAML file (e.g. a *.bak backup) is recorded as IngestSkip
//     with reason non-yaml-file.
//   - A malformed YAML file, or one missing its `id:`, is recorded as an
//     IngestError and does NOT abort the walk.
//   - A duplicate ref surfaced by the underlying registrar is recorded as
//     an IngestError for that file.
//
// IngestCatalog returns an error only when the catalog root itself
// cannot be opened; per-file failures live in the report.
func (f *FileBackedRegistrar) IngestCatalog() (IngestReport, error) {
	report := IngestReport{
		Root:               f.root,
		RegisteredByKind:   map[RegistryKind]int{},
		FilesBySubdir:      map[string]int{},
		RegisteredBySubdir: map[string]int{},
	}

	info, err := os.Stat(f.root)
	if err != nil {
		return report, fmt.Errorf("%w: %v", ErrRegistryCatalogRootUnreadable, err)
	}
	if !info.IsDir() {
		return report, fmt.Errorf("%w: %s is not a directory", ErrRegistryCatalogRootUnreadable, f.root)
	}

	// Walk the union of mapped and unmapped subdirs in a stable order so
	// the report (and the registration order) is deterministic.
	for _, subdir := range f.orderedSubdirs() {
		if err := f.ingestSubdir(subdir, &report); err != nil {
			return report, err
		}
	}
	return report, nil
}

// orderedSubdirs returns every catalog subdir to walk (mapped first,
// then unmapped) sorted within each group for deterministic output.
func (f *FileBackedRegistrar) orderedSubdirs() []string {
	mapped := make([]string, 0, len(catalogSubdirKind))
	for s := range catalogSubdirKind {
		mapped = append(mapped, s)
	}
	sort.Strings(mapped)

	unmapped := make([]string, 0, len(unmappedCatalogSubdirs))
	for s := range unmappedCatalogSubdirs {
		unmapped = append(unmapped, s)
	}
	sort.Strings(unmapped)

	return append(mapped, unmapped...)
}

// ingestSubdir walks one catalog subdirectory non-recursively and folds
// every file into the report. A missing subdirectory is not an error —
// a catalog is allowed to omit any subdir entirely.
func (f *FileBackedRegistrar) ingestSubdir(subdir string, report *IngestReport) error {
	dir := filepath.Join(f.root, subdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("%w: %v", ErrRegistryCatalogRootUnreadable, err)
	}

	kind, mapped := catalogSubdirKind[subdir]

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		path := filepath.Join(dir, name)
		ext := filepath.Ext(name)
		if ext != ".yaml" && ext != ".yml" {
			// Non-YAML clutter (e.g. *.bak-* backups). Record it as a
			// skip so nothing is silently dropped, but do not count it
			// as a YAML "file seen" for reconciliation.
			report.Skipped = append(report.Skipped, IngestSkip{
				FilePath: path,
				Subdir:   subdir,
				Reason:   IngestSkipNonYAML,
				Detail:   "extension " + ext,
			})
			continue
		}

		report.FilesBySubdir[subdir]++

		if !mapped {
			// Recognised catalog subdir with no natural registry kind.
			report.Skipped = append(report.Skipped, IngestSkip{
				FilePath: path,
				Subdir:   subdir,
				Reason:   IngestSkipUnmappedDir,
				Detail:   "subdir " + subdir + " has no registry kind",
			})
			continue
		}

		f.ingestFile(path, subdir, kind, report)
	}
	return nil
}

// ingestFile reads one catalog file, builds a handle-only
// RegistrationRecord, and registers it. Any failure is recorded as an
// IngestError on the report; ingestFile never panics and never aborts
// the surrounding walk.
func (f *FileBackedRegistrar) ingestFile(path, subdir string, kind RegistryKind, report *IngestReport) {
	rec, err := buildRegistrationRecord(path, kind)
	if err != nil {
		report.Errors = append(report.Errors, IngestError{
			FilePath: path,
			Subdir:   subdir,
			Err:      err,
			Message:  err.Error(),
		})
		return
	}

	env := RegistryEnvelope{
		Version:    RegistryEnvelopeVersionV1,
		Resolution: RegistryResolutionLocalFirst,
		Operation:  RegistryOperationRegister,
		Registrar: RegistryRegistrar{
			Mode:     RegistrarModeFileBacked,
			FileRoot: f.root,
		},
		Register: &RegisterPayload{
			Record: rec,
			Upsert: f.upsert,
		},
	}
	if _, err := f.registrar.Handle(env); err != nil {
		report.Errors = append(report.Errors, IngestError{
			FilePath: path,
			Subdir:   subdir,
			Err:      err,
			Message:  err.Error(),
		})
		return
	}

	report.Records = append(report.Records, rec)
	report.RegisteredByKind[kind]++
	report.RegisteredBySubdir[subdir]++
}

// RegistryEnvelopeVersionV1 is the envelope version stamped on
// registrations produced by the file-backed registrar.
const RegistryEnvelopeVersionV1 = "v1alpha1"

// catalogEntryIdentity is the minimal slice of a catalog file the
// file-backed registrar reads. Only the entry's stable `id` is needed to
// build the RegistryObjectRef; the rest of the body stays on disk (D2).
type catalogEntryIdentity struct {
	ID string `yaml:"id"`
}

// buildRegistrationRecord reads a catalog file at path and produces a
// handle-only RegistrationRecord for the given kind.
//
// D2 is enforced here: the file body is read solely to (a) extract the
// entry `id` for the ref name and (b) compute a content digest. The body
// is never placed into the returned RegistrationRecord — RegistrationRecord
// carries Meta (kind + ref + schema/interface) and a RegistrationSource
// pointer (FilePath + Digest + Directory) only.
func buildRegistrationRecord(path string, kind RegistryKind) (RegistrationRecord, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // catalog-sourced path
	if err != nil {
		return RegistrationRecord{}, fmt.Errorf("%w: read %s: %v", ErrRegistryCatalogFileUnreadable, path, err)
	}

	var id catalogEntryIdentity
	if err := yaml.Unmarshal(raw, &id); err != nil {
		return RegistrationRecord{}, fmt.Errorf("%w: %s: %v", ErrRegistryCatalogEntryMalformed, path, err)
	}
	name := strings.TrimSpace(id.ID)
	if name == "" {
		return RegistrationRecord{}, fmt.Errorf("%w: %s: catalog entry has no `id`", ErrRegistryCatalogEntryMissingID, path)
	}

	schemaVersion, iface, ok := KindRegistrationSpec(kind)
	if !ok {
		// Unreachable for the five mapped kinds, kept as a guard against
		// a future mapping that names a kind without a kindSpec.
		return RegistrationRecord{}, fmt.Errorf("%w: %q", ErrRegistryUnknownKind, kind)
	}

	digest := sha256.Sum256(raw)

	rec := RegistrationRecord{
		Meta: RegistryContractMeta{
			Ref:           RegistryObjectRef{Kind: kind, Name: name},
			SchemaVersion: schemaVersion,
			Interface:     iface,
		},
		Source: RegistrationSource{
			FilePath:  path,
			Digest:    "sha256:" + hex.EncodeToString(digest[:]),
			Directory: filepath.Dir(path),
		},
	}
	if err := rec.Validate(); err != nil {
		return RegistrationRecord{}, err
	}
	return rec, nil
}
