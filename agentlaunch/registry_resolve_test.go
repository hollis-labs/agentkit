package agentlaunch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeRuntimeBindingFile drops a RuntimeBindingContract YAML file and returns
// its path — the file a runtime-binding RegistrationRecord points at.
func writeRuntimeBindingFile(t *testing.T, dir, name string) string {
	t.Helper()
	body := "meta:\n" +
		"  ref:\n" +
		"    kind: runtime-binding\n" +
		"    name: " + name + "\n" +
		"  schema_version: " + RuntimeBindingSchemaVersionV1 + "\n" +
		"  interface: " + RuntimeBindingInterfaceV1 + "\n" +
		"binding:\n" +
		"  provider: codex\n" +
		"  model: gpt-5.4\n" +
		"  runtime_kind: jsonrpc-stdio\n" +
		"  args: [\"--sandbox\", \"workspace-write\"]\n" +
		"  timeout: 3h\n"
	path := filepath.Join(dir, name+".yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write runtime-binding file: %v", err)
	}
	return path
}

// registerRuntimeBinding seeds reg with a runtime-binding RegistrationRecord
// (a handle) pointing at filePath.
func registerRuntimeBinding(t *testing.T, reg Registrar, descriptor RegistryRegistrar, name, filePath string) {
	t.Helper()
	rec := RegistrationRecord{
		Meta: RegistryContractMeta{
			Ref:           RegistryObjectRef{Kind: RegistryKindRuntimeBinding, Name: name},
			SchemaVersion: RuntimeBindingSchemaVersionV1,
			Interface:     RuntimeBindingInterfaceV1,
		},
		Source: RegistrationSource{FilePath: filePath},
	}
	if _, err := reg.Handle(RegistryEnvelope{
		Version:    RegistryEnvelopeVersionV1,
		Resolution: RegistryResolutionLocalFirst,
		Operation:  RegistryOperationRegister,
		Registrar:  descriptor,
		Register:   &RegisterPayload{Record: rec},
	}); err != nil {
		t.Fatalf("register runtime-binding %q: %v", name, err)
	}
}

func TestResolveRuntimeBinding(t *testing.T) {
	dir := t.TempDir()
	descriptor := RegistryRegistrar{Mode: RegistrarModeFileBacked, FileRoot: dir}
	reg := NewInMemoryRegistrar()

	path := writeRuntimeBindingFile(t, dir, "codex-cli")
	registerRuntimeBinding(t, reg, descriptor, "codex-cli", path)

	binding, err := ResolveRuntimeBinding(reg, descriptor, "codex-cli")
	if err != nil {
		t.Fatalf("ResolveRuntimeBinding: %v", err)
	}
	if binding.Provider != "codex" {
		t.Errorf("Provider = %q, want codex", binding.Provider)
	}
	if binding.Model != "gpt-5.4" {
		t.Errorf("Model = %q, want gpt-5.4", binding.Model)
	}
	if binding.RuntimeKind != RuntimeJsonRpcStdio {
		t.Errorf("RuntimeKind = %q, want jsonrpc-stdio", binding.RuntimeKind)
	}
	if binding.Timeout != "3h" {
		t.Errorf("Timeout = %q, want 3h", binding.Timeout)
	}

	// The resolved binding must be Validate()-clean — it feeds straight into
	// PlanFromLaunch, which requires a valid RuntimeBinding.
	if err := binding.Validate(); err != nil {
		t.Errorf("resolved binding must validate: %v", err)
	}
}

func TestResolveRuntimeBinding_NotFound(t *testing.T) {
	dir := t.TempDir()
	descriptor := RegistryRegistrar{Mode: RegistrarModeFileBacked, FileRoot: dir}
	reg := NewInMemoryRegistrar()

	_, err := ResolveRuntimeBinding(reg, descriptor, "no-such-runner")
	if !errors.Is(err, ErrRuntimeBindingNotFound) {
		t.Fatalf("err = %v, want ErrRuntimeBindingNotFound", err)
	}
}

func TestResolveRuntimeBinding_EmptyRunner(t *testing.T) {
	dir := t.TempDir()
	descriptor := RegistryRegistrar{Mode: RegistrarModeFileBacked, FileRoot: dir}
	if _, err := ResolveRuntimeBinding(NewInMemoryRegistrar(), descriptor, ""); err == nil {
		t.Error("empty runner id should error")
	}
}

func TestResolveRuntimeBinding_MissingSourceFile(t *testing.T) {
	dir := t.TempDir()
	descriptor := RegistryRegistrar{Mode: RegistrarModeFileBacked, FileRoot: dir}
	reg := NewInMemoryRegistrar()

	// Register a handle pointing at a file that does not exist.
	registerRuntimeBinding(t, reg, descriptor, "ghost", filepath.Join(dir, "ghost.yaml"))

	if _, err := ResolveRuntimeBinding(reg, descriptor, "ghost"); err == nil {
		t.Error("a record whose source file is missing should error")
	}
}
