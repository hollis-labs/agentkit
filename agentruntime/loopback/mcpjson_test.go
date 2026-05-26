package loopback

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRenderMCPJSONSubprocessDescriptor(t *testing.T) {
	got, err := RenderMCPJSONString([]Descriptor{{
		Name:    "nanite",
		Kind:    Subprocess,
		Command: "/usr/local/bin/nanite",
		Args:    []string{"mcp", "--db", "/data/nanite.db", "--session", "sess-1"},
	}}, MCPJSONOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "mcpServers": {
    "nanite": {
      "command": "/usr/local/bin/nanite",
      "args": [
        "mcp",
        "--db",
        "/data/nanite.db",
        "--session",
        "sess-1"
      ],
      "env": {}
    }
  }
}`
	if got != want {
		t.Fatalf("rendered .mcp.json mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestRenderMCPJSONEnvAndPreservesValues(t *testing.T) {
	body, err := RenderMCPJSON([]Descriptor{{
		Name:    "nanite",
		Kind:    Subprocess,
		Command: "/bin/nanite",
		Args:    []string{"mcp", "--db", "/db"},
		Env:     map[string]string{"NANITE_API_URL": "http://127.0.0.1:8090"},
	}}, MCPJSONOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	server := decoded.MCPServers["nanite"]
	if server.Command != "/bin/nanite" {
		t.Fatalf("command = %q", server.Command)
	}
	if !reflect.DeepEqual(server.Args, []string{"mcp", "--db", "/db"}) {
		t.Fatalf("args = %#v", server.Args)
	}
	if server.Env["NANITE_API_URL"] != "http://127.0.0.1:8090" {
		t.Fatalf("env = %#v", server.Env)
	}
}

func TestRenderMCPJSONMultipleDescriptorsDeterministic(t *testing.T) {
	descriptors := []Descriptor{
		{Name: "zeta", Kind: Subprocess, Command: "/bin/z"},
		{Name: "alpha", Kind: Subprocess, Command: "/bin/a"},
	}
	first, err := RenderMCPJSONString(descriptors, MCPJSONOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderMCPJSONString(descriptors, MCPJSONOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("rendering is not deterministic\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if strings.Index(first, `"alpha"`) > strings.Index(first, `"zeta"`) {
		t.Fatalf("server names not sorted in output:\n%s", first)
	}
}

func TestRenderMCPJSONRejectsUnsupportedKinds(t *testing.T) {
	cases := []Descriptor{
		{Name: "http", Kind: HTTPSSE, URL: "http://127.0.0.1:1/sse"},
		{Name: "mux", Kind: MuxProxy, Command: "/bin/mux"},
	}
	for _, desc := range cases {
		t.Run(string(desc.Kind), func(t *testing.T) {
			_, err := RenderMCPJSON([]Descriptor{desc}, MCPJSONOptions{})
			if !errors.Is(err, ErrUnsupportedDescriptorKind) {
				t.Fatalf("err = %v, want ErrUnsupportedDescriptorKind", err)
			}
		})
	}
}

func TestRenderMCPJSONRejectsInvalidDescriptor(t *testing.T) {
	_, err := RenderMCPJSON([]Descriptor{{Name: "bad", Kind: Subprocess}}, MCPJSONOptions{})
	if !errors.Is(err, ErrInvalidDescriptor) {
		t.Fatalf("err = %v, want ErrInvalidDescriptor", err)
	}
}

func TestRenderMCPJSONEmptyListAndNilArgs(t *testing.T) {
	empty, err := RenderMCPJSONString(nil, MCPJSONOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if empty != "{\n  \"mcpServers\": {}\n}" {
		t.Fatalf("empty render = %q", empty)
	}

	body, err := RenderMCPJSON([]Descriptor{{Name: "empty", Kind: Subprocess, Command: "/bin/empty"}}, MCPJSONOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		MCPServers map[string]struct {
			Args []string          `json:"args"`
			Env  map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	server := decoded.MCPServers["empty"]
	if server.Args == nil || len(server.Args) != 0 {
		t.Fatalf("nil args should render as empty array, got %#v", server.Args)
	}
	if server.Env == nil || len(server.Env) != 0 {
		t.Fatalf("nil env should render as empty object, got %#v", server.Env)
	}
}
