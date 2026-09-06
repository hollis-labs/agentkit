package agentlaunch_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedMaterializationFixturesAreLoadable(t *testing.T) {
	fixtures := []string{
		"cairn-install.json",
		"nanite-refresh.json",
		"torque-loopback-task.json",
		"tether-multi-task-tree.json",
	}
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "shared-materialization", name))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			var doc struct {
				Fixture string `json:"fixture"`
				Source  struct {
					App      string   `json:"app"`
					Checkout string   `json:"checkout"`
					Head     string   `json:"head"`
					Paths    []string `json:"paths"`
				} `json:"source"`
				Artifacts []struct {
					Path string `json:"path"`
					Kind string `json:"kind"`
					Mode string `json:"mode"`
				} `json:"artifacts"`
			}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			if doc.Fixture == "" || doc.Source.App == "" || doc.Source.Checkout == "" || doc.Source.Head == "" {
				t.Fatalf("fixture missing provenance: %+v", doc)
			}
			if len(doc.Source.Paths) == 0 {
				t.Fatal("fixture has no source paths")
			}
			if len(doc.Artifacts) == 0 {
				t.Fatal("fixture has no artifacts")
			}
		})
	}
}
