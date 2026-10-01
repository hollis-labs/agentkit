package turn

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	permission "github.com/hollis-labs/go-permission"
)

// Codex app-server approval requests. For an action its approval policy
// gates, Codex sends a server-initiated JSON-RPC request (a frame carrying
// both a method and an id) and blocks that action until the client answers.
const (
	// CodexCommandApprovalMethod asks to run a command the sandbox would
	// otherwise refuse (outside the writable roots, or with network access).
	CodexCommandApprovalMethod = "item/commandExecution/requestApproval"
	// CodexFileChangeApprovalMethod asks to apply a file change the sandbox
	// would otherwise refuse (a write outside the writable roots).
	CodexFileChangeApprovalMethod = "item/fileChange/requestApproval"
	// CodexElicitationMethod is an MCP elicitation. Codex uses it to gate MCP
	// tool calls, marking those with _meta.codex_approval_kind.
	CodexElicitationMethod = "mcpServer/elicitation/request"
)

// CodexApprovalKind classifies a Codex approval request.
type CodexApprovalKind string

const (
	CodexApprovalCommandExecution CodexApprovalKind = "command_execution"
	CodexApprovalFileChange       CodexApprovalKind = "file_change"
	// CodexApprovalMCPToolCall is also the _meta.codex_approval_kind value
	// that marks an elicitation as an MCP tool-call approval.
	CodexApprovalMCPToolCall CodexApprovalKind = "mcp_tool_call"
)

// CodexApprovalResponder answers Codex app-server approval requests from a
// permission posture, with no human in the loop. Hook returns a function for
// agentsessions.StartOptions.JsonRpcRequestHook; Decide returns the full
// outcome for a caller that also reports it.
//
// The posture is go-permission's Mode. What each mode grants:
//
//	request                  default  accept-edits  plan     yolo
//	MCP tool call            accept   accept        decline  accept
//	file change              decline  accept        decline  accept
//	command execution        decline  decline       decline  accept
//
// A request this responder cannot answer for a human gets a JSON-RPC error
// instead of a decision, in every mode: any other method (user-input
// questions, permission-profile requests, dynamic tool calls, auth refresh,
// the v1 approval methods), an elicitation that is not an MCP tool-call
// approval, and params it cannot read. Codex reads the error as a refusal and
// fails the action, so an approval kind added after this was written shows up
// as a visible failure rather than being granted by a policy that never
// considered it.
//
// Default mode, headless: the agent can call the MCP tools its launch planted
// for it, and every sandbox escalation is declined. Codex only asks about a
// command or a file change when it would leave the sandbox, and that is a
// per-call decision nobody is present to make. MCP tool calls are approved
// because a launched worker has no human to ask; leaving that gate closed
// makes the planted tools unusable rather than safer, and real authorization
// for them stays with the MCP server and the scopes it was launched with.
//
// MCPAllow narrows that: when it has entries, default and accept-edits
// approve an MCP tool call only if its server and tool match an entry, and
// decline it otherwise. Plan still declines every call and yolo still
// approves every call. With no entries every MCP tool call is approved as
// above, so a host that plants a server exposing high-impact tools lists
// what its workers may call instead of carving out its own hook.
//
// "Decline" lets the agent continue its turn and try something else; the
// responder never cancels a turn. The zero Mode is ModeDefault. An unknown
// mode declines everything, and Validate reports it.
type CodexApprovalResponder struct {
	Mode permission.Mode

	// MCPAllow lists the MCP tool calls default and accept-edits may
	// approve. Each entry is "server", for every tool on that server, or
	// "server/tool". Both halves are path.Match patterns, so "mux/torque_*"
	// allows the mux server's torque_ tools and nothing else on it. Names
	// are matched case-sensitively, as Codex sends them: the server is the
	// elicitation's serverName (the [mcp_servers.<name>] key it was planted
	// under), and the tool is _meta.tool_name or, since Codex 0.159.2 sends
	// none, the name quoted in its approval message. A call whose tool name
	// cannot be read matches only "server" entries.
	MCPAllow []string
}

// CodexApprovalOutcome is how a CodexApprovalResponder answered one request.
type CodexApprovalOutcome struct {
	// Method is the request method.
	Method string
	// Kind is the approval kind, or empty when the request was not an
	// approval this responder decides (Err is then set).
	Kind CodexApprovalKind
	// Allowed reports whether the request was granted. It is false whenever
	// Err is set.
	Allowed bool
	// Reason says why, in a form suitable for a log or an event payload.
	Reason string
	// Result is the JSON-RPC result to send when Err is nil.
	Result any
	// Err is the JSON-RPC error to send instead of a result.
	Err *agentsessions.JsonRpcError

	// MCPServer and MCPTool name the MCP tool call an elicitation asked
	// about, as far as they could be read; empty for other requests.
	MCPServer string
	MCPTool   string
	// MCPAllowEntry is the MCPAllow entry that granted the call, when one
	// did.
	MCPAllowEntry string
}

// Response returns the outcome in the shape JsonRpcRequestHook returns.
func (o CodexApprovalOutcome) Response() (any, *agentsessions.JsonRpcError) {
	if o.Err != nil {
		return nil, o.Err
	}
	return o.Result, nil
}

// Validate reports an unknown Mode or a malformed MCPAllow entry. The empty
// Mode is valid and means ModeDefault.
func (r CodexApprovalResponder) Validate() error {
	switch r.Mode {
	case "", permission.ModeDefault, permission.ModeAcceptEdits, permission.ModePlan, permission.ModeYolo:
	default:
		return fmt.Errorf("turn: unknown permission mode %q", r.Mode)
	}
	for _, entry := range r.MCPAllow {
		server, tool, hasTool := strings.Cut(entry, "/")
		if server == "" || (hasTool && tool == "") {
			return fmt.Errorf("turn: MCP allow-list entry %q: want \"server\" or \"server/tool\"", entry)
		}
		for _, pattern := range []string{server, tool} {
			if _, err := path.Match(pattern, ""); err != nil {
				return fmt.Errorf("turn: MCP allow-list entry %q: %w", entry, err)
			}
		}
	}
	return nil
}

// Hook returns a function for agentsessions.StartOptions.JsonRpcRequestHook.
func (r CodexApprovalResponder) Hook() func(method string, params json.RawMessage) (any, *agentsessions.JsonRpcError) {
	return func(method string, params json.RawMessage) (any, *agentsessions.JsonRpcError) {
		return r.Decide(method, params).Response()
	}
}

// Decide answers one server-initiated request.
func (r CodexApprovalResponder) Decide(method string, params json.RawMessage) CodexApprovalOutcome {
	switch method {
	case CodexCommandApprovalMethod:
		if err := decodeCodexParams(params, &struct{}{}); err != nil {
			return codexInvalidParams(method, err)
		}
		return r.decide(method, CodexApprovalCommandExecution, codexDecisionResult)
	case CodexFileChangeApprovalMethod:
		if err := decodeCodexParams(params, &struct{}{}); err != nil {
			return codexInvalidParams(method, err)
		}
		return r.decide(method, CodexApprovalFileChange, codexDecisionResult)
	case CodexElicitationMethod:
		var p struct {
			ServerName string `json:"serverName"`
			Message    string `json:"message"`
			Meta       struct {
				ApprovalKind string `json:"codex_approval_kind"`
				ToolName     string `json:"tool_name"`
			} `json:"_meta"`
		}
		if err := decodeCodexParams(params, &p); err != nil {
			return codexInvalidParams(method, err)
		}
		if CodexApprovalKind(p.Meta.ApprovalKind) != CodexApprovalMCPToolCall {
			return codexUnhandled(method, fmt.Sprintf("elicitation kind %q is not an MCP tool-call approval", p.Meta.ApprovalKind))
		}
		tool := p.Meta.ToolName
		if tool == "" {
			tool = codexMCPToolFromMessage(p.ServerName, p.Message)
		}
		return r.decideMCP(method, p.ServerName, tool)
	default:
		return codexUnhandled(method, "not a Codex approval request")
	}
}

func (r CodexApprovalResponder) decide(method string, kind CodexApprovalKind, result func(allowed bool) any) CodexApprovalOutcome {
	allowed, reason := codexApprovalAllowed(r.Mode, kind)
	return CodexApprovalOutcome{
		Method:  method,
		Kind:    kind,
		Allowed: allowed,
		Reason:  reason,
		Result:  result(allowed),
	}
}

// decideMCP applies the posture table, then MCPAllow, to an MCP tool call.
func (r CodexApprovalResponder) decideMCP(method, server, tool string) CodexApprovalOutcome {
	out := r.decide(method, CodexApprovalMCPToolCall, codexElicitationResult)
	out.MCPServer, out.MCPTool = server, tool
	if !out.Allowed || r.Mode == permission.ModeYolo || len(r.MCPAllow) == 0 {
		return out
	}
	mode := r.Mode
	if mode == "" {
		mode = permission.ModeDefault
	}
	call := server + "/" + tool
	if tool == "" {
		call = server + " (tool name unreadable)"
	}
	if entry, ok := matchMCPAllow(r.MCPAllow, server, tool); ok {
		out.MCPAllowEntry = entry
		out.Reason = fmt.Sprintf("%s mode: MCP tool call %s approved by allow-list entry %q", mode, call, entry)
		return out
	}
	out.Allowed = false
	out.Reason = fmt.Sprintf("%s mode: MCP tool call %s is not on the MCP allow-list, declined", mode, call)
	out.Result = codexElicitationResult(false)
	return out
}

// matchMCPAllow returns the first entry that allows server/tool. An entry
// naming a tool never matches a call whose tool is unknown, and nothing
// matches a call with no server name.
func matchMCPAllow(entries []string, server, tool string) (string, bool) {
	if server == "" {
		return "", false
	}
	for _, entry := range entries {
		serverPattern, toolPattern, hasTool := strings.Cut(entry, "/")
		if ok, _ := path.Match(serverPattern, server); !ok {
			continue
		}
		if !hasTool {
			return entry, true
		}
		if tool == "" {
			continue
		}
		if ok, _ := path.Match(toolPattern, tool); ok {
			return entry, true
		}
	}
	return "", false
}

// codexMCPToolFromMessage reads the tool name from the approval message
// Codex builds for an MCP server's tool call: Allow the <server> MCP server
// to run tool "<tool>"? (codex-cli 0.154.0 to 0.159.2, which send no
// _meta.tool_name). The name is quoted as the tool's name even when the
// tool has a title. Any other message yields "".
func codexMCPToolFromMessage(server, message string) string {
	prefix := "Allow the " + server + " MCP server to run tool \""
	const suffix = "\"?"
	if server == "" || !strings.HasPrefix(message, prefix) || !strings.HasSuffix(message, suffix) || len(message) <= len(prefix)+len(suffix) {
		return ""
	}
	return message[len(prefix) : len(message)-len(suffix)]
}

// codexApprovalAllowed is the posture table in CodexApprovalResponder's doc.
func codexApprovalAllowed(mode permission.Mode, kind CodexApprovalKind) (bool, string) {
	switch mode {
	case permission.ModeYolo:
		return true, "yolo mode: every approval is granted"
	case permission.ModePlan:
		return false, "plan mode: read-only, " + string(kind) + " declined"
	case permission.ModeAcceptEdits:
		switch kind {
		case CodexApprovalMCPToolCall:
			return true, "accept-edits mode: MCP tool call approved"
		case CodexApprovalFileChange:
			return true, "accept-edits mode: file change approved"
		default:
			return false, "accept-edits mode: " + string(kind) + " needs a human, declined"
		}
	case "", permission.ModeDefault:
		if kind == CodexApprovalMCPToolCall {
			return true, "default mode: MCP tool call approved"
		}
		return false, "default mode: " + string(kind) + " is a sandbox escalation and needs a human, declined"
	default:
		return false, fmt.Sprintf("unknown permission mode %q: %s declined", mode, kind)
	}
}

// codexDecisionResult is the response to a command or file-change approval.
// "decline" lets the agent continue the turn; "cancel" would also interrupt
// it, which this responder never does.
func codexDecisionResult(allowed bool) any {
	if allowed {
		return map[string]any{"decision": "accept"}
	}
	return map[string]any{"decision": "decline"}
}

// codexElicitationResult is the MCP elicitation response. An accept always
// carries content: Codex asks for an object even when the requested schema
// is empty, and a nil map would marshal to null.
func codexElicitationResult(allowed bool) any {
	if allowed {
		return map[string]any{"action": "accept", "content": map[string]any{}}
	}
	return map[string]any{"action": "decline"}
}

func decodeCodexParams(params json.RawMessage, into any) error {
	if len(params) == 0 {
		return nil
	}
	return json.Unmarshal(params, into)
}

func codexUnhandled(method, why string) CodexApprovalOutcome {
	return CodexApprovalOutcome{
		Method: method,
		Reason: why,
		Err: &agentsessions.JsonRpcError{
			Code:    -32601,
			Message: "agentkit: codex approval responder does not handle " + method + ": " + why,
		},
	}
}

// codexInvalidParams refuses a request it could not read rather than grant
// something it did not understand.
func codexInvalidParams(method string, err error) CodexApprovalOutcome {
	return CodexApprovalOutcome{
		Method: method,
		Reason: "unreadable params: " + err.Error(),
		Err: &agentsessions.JsonRpcError{
			Code:    -32602,
			Message: "agentkit: codex approval responder could not read " + method + " params: " + err.Error(),
		},
	}
}
