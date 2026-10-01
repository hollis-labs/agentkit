package turn

import (
	"encoding/json"
	"strings"
	"testing"

	permission "github.com/hollis-labs/go-permission"
)

// codexMCPToolCallElicitation is the params block codex-cli 0.154.0 sent for
// an MCP tool-call approval, captured from a live app-server session by
// Tether (internal/app/codex_approval_test.go) on 2026-09-12. Kept verbatim
// so the responder is tested against the real shape it keys on.
const codexMCPToolCallElicitation = `{
  "threadId": "01a095f4-371e-7992-8cdb-7904962af41f",
  "turnId": "01a095f4-3750-78a2-a722-ebd832593249",
  "serverName": "mux",
  "mode": "form",
  "_meta": {
    "codex_approval_kind": "mcp_tool_call",
    "persist": ["session", "always"],
    "tool_description": "Health check for the agent-mux MCP adapter.",
    "tool_params": {},
    "tool_params_display": []
  },
  "message": "Allow the mux MCP server to run tool \"mux_health\"?",
  "requestedSchema": {"type": "object", "properties": {}}
}`

const codexCommandApproval = `{"threadId":"t","turnId":"u","itemId":"i","command":"curl https://example.com","cwd":"/work","reason":"network access"}`

const codexFileChangeApproval = `{"threadId":"t","turnId":"u","itemId":"i","grantRoot":"/etc","reason":"write outside workspace"}`

func TestCodexApprovalResponderPostureTable(t *testing.T) {
	requests := []struct {
		kind   CodexApprovalKind
		method string
		params string
	}{
		{CodexApprovalMCPToolCall, CodexElicitationMethod, codexMCPToolCallElicitation},
		{CodexApprovalFileChange, CodexFileChangeApprovalMethod, codexFileChangeApproval},
		{CodexApprovalCommandExecution, CodexCommandApprovalMethod, codexCommandApproval},
	}
	// want[mode][kind] is the posture table in CodexApprovalResponder's doc.
	want := map[permission.Mode]map[CodexApprovalKind]bool{
		"": {
			CodexApprovalMCPToolCall: true, CodexApprovalFileChange: false, CodexApprovalCommandExecution: false,
		},
		permission.ModeDefault: {
			CodexApprovalMCPToolCall: true, CodexApprovalFileChange: false, CodexApprovalCommandExecution: false,
		},
		permission.ModeAcceptEdits: {
			CodexApprovalMCPToolCall: true, CodexApprovalFileChange: true, CodexApprovalCommandExecution: false,
		},
		permission.ModePlan: {
			CodexApprovalMCPToolCall: false, CodexApprovalFileChange: false, CodexApprovalCommandExecution: false,
		},
		permission.ModeYolo: {
			CodexApprovalMCPToolCall: true, CodexApprovalFileChange: true, CodexApprovalCommandExecution: true,
		},
		"bypassPermissions": {
			CodexApprovalMCPToolCall: false, CodexApprovalFileChange: false, CodexApprovalCommandExecution: false,
		},
	}
	for mode, byKind := range want {
		for _, req := range requests {
			t.Run(string(mode)+"/"+string(req.kind), func(t *testing.T) {
				out := CodexApprovalResponder{Mode: mode}.Decide(req.method, json.RawMessage(req.params))
				if out.Err != nil {
					t.Fatalf("Err = %v, want a decision", out.Err)
				}
				if out.Kind != req.kind {
					t.Errorf("Kind = %q, want %q", out.Kind, req.kind)
				}
				if out.Allowed != byKind[req.kind] {
					t.Errorf("Allowed = %v, want %v (reason %q)", out.Allowed, byKind[req.kind], out.Reason)
				}
				if out.Reason == "" {
					t.Error("Reason is empty")
				}
				result, rpcErr := out.Response()
				if rpcErr != nil {
					t.Fatalf("Response error = %v", rpcErr)
				}
				encoded, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				if got, want := string(encoded), codexWantWire(req.kind, out.Allowed); got != want {
					t.Errorf("wire form = %s, want %s", got, want)
				}
			})
		}
	}
}

func codexWantWire(kind CodexApprovalKind, allowed bool) string {
	switch {
	case kind == CodexApprovalMCPToolCall && allowed:
		// content is required even for an empty requested schema; a nil map
		// would marshal to null.
		return `{"action":"accept","content":{}}`
	case kind == CodexApprovalMCPToolCall:
		return `{"action":"decline"}`
	case allowed:
		return `{"decision":"accept"}`
	default:
		// decline, never cancel: the agent keeps its turn.
		return `{"decision":"decline"}`
	}
}

// A request the responder cannot answer for a human must come back as an
// error, in every mode including yolo. Returning (nil, nil) would marshal to a
// successful null result, which Codex can read as consent.
func TestCodexApprovalResponderRefusesWhatItDoesNotDecide(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		params   string
		wantCode int
	}{
		{"user input question", "item/tool/requestUserInput", `{"questions":[]}`, -32601},
		{"permission profile request", "item/permissions/requestApproval", `{"permissions":{}}`, -32601},
		{"dynamic tool call", "item/tool/call", `{}`, -32601},
		{"auth refresh", "account/chatgptAuthTokens/refresh", `{}`, -32601},
		{"v1 exec approval", "execCommandApproval", `{}`, -32601},
		{"elicitation for another approval kind", CodexElicitationMethod, `{"serverName":"mux","_meta":{"codex_approval_kind":"exec_escalation"}}`, -32601},
		{"elicitation with no approval kind", CodexElicitationMethod, `{"serverName":"mux","message":"What is your name?"}`, -32601},
		{"malformed elicitation params", CodexElicitationMethod, `{"_meta":"not-an-object"}`, -32602},
		{"malformed command params", CodexCommandApprovalMethod, `["not","an","object"]`, -32602},
		{"malformed file change params", CodexFileChangeApprovalMethod, `"nope"`, -32602},
	}
	for _, mode := range []permission.Mode{permission.ModeDefault, permission.ModeYolo} {
		for _, tc := range cases {
			t.Run(string(mode)+"/"+tc.name, func(t *testing.T) {
				hook := CodexApprovalResponder{Mode: mode}.Hook()
				result, rpcErr := hook(tc.method, json.RawMessage(tc.params))
				if rpcErr == nil {
					t.Fatalf("got result %v, want an error", result)
				}
				if result != nil {
					t.Errorf("result = %v, want nil alongside the error", result)
				}
				if rpcErr.Code != tc.wantCode {
					t.Errorf("code = %d, want %d", rpcErr.Code, tc.wantCode)
				}
				if !strings.Contains(rpcErr.Message, tc.method) {
					t.Errorf("message %q does not name the method", rpcErr.Message)
				}
			})
		}
	}
}

func TestCodexApprovalResponderValidate(t *testing.T) {
	for _, mode := range []permission.Mode{"", permission.ModeDefault, permission.ModeAcceptEdits, permission.ModePlan, permission.ModeYolo} {
		if err := (CodexApprovalResponder{Mode: mode}).Validate(); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", mode, err)
		}
	}
	for _, mode := range []permission.Mode{"bypass", "acceptEdits", "YOLO"} {
		if err := (CodexApprovalResponder{Mode: mode}).Validate(); err == nil {
			t.Errorf("Validate(%q) = nil, want an error", mode)
		}
	}
}

// codexMCPToolCallElicitationTitled is the params block codex-cli 0.159.2
// sent for an MCP tool-call approval of a tool that declares a title,
// captured from a live app-server session on 2026-10-01 (CW-20261001-0084).
// Codex sends no _meta.tool_name; the message quotes the tool's name, not its
// title, which arrives separately as _meta.tool_title.
const codexMCPToolCallElicitationTitled = `{
  "threadId": "01a0f5d0-fc55-7cb1-915d-651ca93c59dd",
  "turnId": "01a0f5d0-fce9-7da1-957c-2cf701a8ba3e",
  "serverName": "probe",
  "mode": "form",
  "_meta": {
    "codex_approval_kind": "mcp_tool_call",
    "persist": ["session", "always"],
    "tool_title": "Probe Echo Title",
    "tool_description": "Echo the given text back.",
    "tool_params": {"text": "hello"},
    "tool_params_display": [{"name": "text", "value": "hello", "display_name": "text"}]
  },
  "message": "Allow the probe MCP server to run tool \"probe_echo\"?",
  "requestedSchema": {"type": "object", "properties": {}}
}`

func TestCodexApprovalResponderMCPAllowList(t *testing.T) {
	const (
		toolNameInMeta = `{"serverName":"mux","message":"Allow the mux MCP server to run tool \"ignored\"?","_meta":{"codex_approval_kind":"mcp_tool_call","tool_name":"cerberus_ssh_exec"}}`
		unreadableTool = `{"serverName":"mux","message":"Approve app tool call?","_meta":{"codex_approval_kind":"mcp_tool_call"}}`
		noServerName   = `{"message":"Allow the  MCP server to run tool \"x\"?","_meta":{"codex_approval_kind":"mcp_tool_call"}}`
	)
	cases := []struct {
		name      string
		mode      permission.Mode
		allow     []string
		params    string
		wantAllow bool
		wantEntry string
		wantTool  string
	}{
		{"no list approves every planted server (unchanged)", permission.ModeDefault, nil, codexMCPToolCallElicitation, true, "", "mux_health"},
		{"server entry", permission.ModeDefault, []string{"mux"}, codexMCPToolCallElicitation, true, "mux", "mux_health"},
		{"server/tool entry", "", []string{"probe", "mux/mux_health"}, codexMCPToolCallElicitation, true, "mux/mux_health", "mux_health"},
		{"tool glob", permission.ModeAcceptEdits, []string{"mux/mux_*"}, codexMCPToolCallElicitation, true, "mux/mux_*", "mux_health"},
		{"tool glob that does not match", permission.ModeDefault, []string{"mux/torque_*"}, codexMCPToolCallElicitation, false, "", "mux_health"},
		{"other server only", permission.ModeAcceptEdits, []string{"probe"}, codexMCPToolCallElicitation, false, "", "mux_health"},
		{"0.159.2 titled tool matches by name", permission.ModeDefault, []string{"probe/probe_echo"}, codexMCPToolCallElicitationTitled, true, "probe/probe_echo", "probe_echo"},
		{"0.159.2 titled tool does not match by title", permission.ModeDefault, []string{"probe/Probe Echo Title"}, codexMCPToolCallElicitationTitled, false, "", "probe_echo"},
		{"_meta.tool_name wins over the message", permission.ModeDefault, []string{"mux/mux_*"}, toolNameInMeta, false, "", "cerberus_ssh_exec"},
		{"unreadable tool matches a server entry", permission.ModeDefault, []string{"mux"}, unreadableTool, true, "mux", ""},
		{"unreadable tool never matches a tool entry", permission.ModeDefault, []string{"mux/*"}, unreadableTool, false, "", ""},
		{"no server name matches nothing", permission.ModeDefault, []string{"*"}, noServerName, false, "", ""},
		{"yolo ignores the list", permission.ModeYolo, []string{"probe"}, codexMCPToolCallElicitation, true, "", "mux_health"},
		{"plan declines a listed call", permission.ModePlan, []string{"mux"}, codexMCPToolCallElicitation, false, "", "mux_health"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := CodexApprovalResponder{Mode: tc.mode, MCPAllow: tc.allow}
			if err := r.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			out := r.Decide(CodexElicitationMethod, json.RawMessage(tc.params))
			if out.Err != nil {
				t.Fatalf("Err = %v, want a decision", out.Err)
			}
			if out.Allowed != tc.wantAllow {
				t.Errorf("Allowed = %v, want %v (reason %q)", out.Allowed, tc.wantAllow, out.Reason)
			}
			if out.MCPAllowEntry != tc.wantEntry {
				t.Errorf("MCPAllowEntry = %q, want %q", out.MCPAllowEntry, tc.wantEntry)
			}
			if out.MCPTool != tc.wantTool {
				t.Errorf("MCPTool = %q, want %q", out.MCPTool, tc.wantTool)
			}
			if len(tc.allow) > 0 && tc.mode != permission.ModeYolo && tc.mode != permission.ModePlan && !strings.Contains(out.Reason, "allow-list") {
				t.Errorf("Reason %q does not mention the allow-list", out.Reason)
			}
			result, _ := out.Response()
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := string(encoded), codexWantWire(CodexApprovalMCPToolCall, tc.wantAllow); got != want {
				t.Errorf("wire form = %s, want %s", got, want)
			}
		})
	}
}

// The allow-list narrows MCP tool calls only; it never touches the sandbox
// escalations the posture table decides.
func TestCodexApprovalResponderMCPAllowListLeavesOtherKindsAlone(t *testing.T) {
	r := CodexApprovalResponder{Mode: permission.ModeAcceptEdits, MCPAllow: []string{"probe"}}
	if out := r.Decide(CodexFileChangeApprovalMethod, json.RawMessage(codexFileChangeApproval)); !out.Allowed {
		t.Errorf("accept-edits file change with an MCP allow-list: Allowed = false (%s)", out.Reason)
	}
	if out := r.Decide(CodexCommandApprovalMethod, json.RawMessage(codexCommandApproval)); out.Allowed {
		t.Errorf("accept-edits command with an MCP allow-list: Allowed = true (%s)", out.Reason)
	}
}

func TestCodexApprovalResponderValidateMCPAllow(t *testing.T) {
	for _, entry := range []string{"mux", "mux/torque_task_get", "mux/torque_*", "*", "m?x/[a-z]*"} {
		if err := (CodexApprovalResponder{MCPAllow: []string{entry}}).Validate(); err != nil {
			t.Errorf("Validate(MCPAllow %q) = %v, want nil", entry, err)
		}
	}
	for _, entry := range []string{"", "/tool", "mux/", "[", "mux/[a-"} {
		if err := (CodexApprovalResponder{MCPAllow: []string{entry}}).Validate(); err == nil {
			t.Errorf("Validate(MCPAllow %q) = nil, want an error", entry)
		}
	}
}
