package loopback

import (
	"encoding/json"
	"fmt"
)

type MCPJSONOptions struct {
	Prefix string
	Indent string
}

type mcpJSONDocument struct {
	MCPServers map[string]mcpJSONServer `json:"mcpServers"`
}

type mcpJSONServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// RenderMCPJSON renders subprocess descriptors into the .mcp.json shape
// consumed by Claude, Codex, and Opencode MCP clients. Empty descriptor lists
// render a valid empty config: {"mcpServers":{}}. Nil Args render as []; nil
// Env renders as {}.
func RenderMCPJSON(descriptors []Descriptor, opts MCPJSONOptions) ([]byte, error) {
	doc := mcpJSONDocument{MCPServers: map[string]mcpJSONServer{}}
	for _, desc := range descriptors {
		if err := desc.Validate(); err != nil {
			return nil, err
		}
		if desc.Kind != Subprocess {
			return nil, fmt.Errorf("%w: %s", ErrUnsupportedDescriptorKind, desc.Kind)
		}
		args := append([]string(nil), desc.Args...)
		if args == nil {
			args = []string{}
		}
		env := cloneEnv(desc.Env)
		if env == nil {
			env = map[string]string{}
		}
		doc.MCPServers[desc.Name] = mcpJSONServer{
			Command: desc.Command,
			Args:    args,
			Env:     env,
		}
	}
	indent := opts.Indent
	if indent == "" {
		indent = "  "
	}
	return json.MarshalIndent(doc, opts.Prefix, indent)
}

func RenderMCPJSONString(descriptors []Descriptor, opts MCPJSONOptions) (string, error) {
	data, err := RenderMCPJSON(descriptors, opts)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func cloneEnv(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
