// Package turn frames user turns before they are sent to go-agent-sessions.
//
// It also carries the Codex app-server client protocol shared by every app:
// CodexAppServerSession and CodexAppServerCache bind a thread (starting one,
// or resuming a stored one) and send turns on it, the Codex notification
// parsers read what comes back, and CodexApprovalResponder answers the
// approval requests Codex sends the client, from a go-permission Mode.
package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
)

var (
	ErrUnsupportedRuntime = errors.New("turn: unsupported runtime")
	ErrMissingThreadID    = errors.New("turn: missing codex thread id")
	// ErrCodexThreadMismatch is the Err of the SessionLostError returned when
	// thread/resume answers with a thread other than the one requested.
	ErrCodexThreadMismatch = errors.New("turn: codex resumed a different thread than requested")
)

type Options struct {
	Provider string
	Runtime  runtimes.Mode
	// JSONRPCMethod is a provider-specific wire detail. Public callers should
	// prefer SendTurn and let the provider binding/adapter choose the real
	// JSON-RPC method. Tests and custom adapters can override it here.
	JSONRPCMethod string
}

type Sender interface {
	SendInput(ctx context.Context, data []byte) error
}

type JSONRPCSender interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
}

// Frame returns the payload for raw SendInput. JSON-RPC stdio callers should
// prefer SendTurn so typed calls do not go through the raw byte escape hatch.
//
// An ACP mode frames plain text: the ACP client the session runs behind
// (go-agent-wrapper's acp.Manager) builds session/prompt from it, so the
// caller never writes ACP JSON-RPC itself. An empty mode (an API binding) has
// no process to frame for and is ErrUnsupportedRuntime.
func Frame(text string, opts Options) ([]byte, error) {
	switch opts.Runtime {
	case runtimes.ModeStreamingStdio:
		return ClaudeStreamingUserFrame(text)
	case runtimes.ModeSubprocessPerTurn, runtimes.ModeHTTPSSE, runtimes.ModePTY,
		runtimes.ModeACPStdio, runtimes.ModeACPTCP:
		return []byte(text), nil
	case runtimes.ModeJSONRPCStdio:
		params := map[string]any{"message": text}
		return json.Marshal(map[string]any{"method": method(opts), "params": params})
	default:
		return nil, ErrUnsupportedRuntime
	}
}

// SendTurn applies runtime-specific framing and delivery. JSON-RPC stdio uses a
// typed call when the sender exposes JSONRPCSender; otherwise it falls back to a
// serialized request-shaped frame for adapters that own the final method.
// Codex app-server callers should use CodexAppServerSession or
// CodexAppServerCache so the initialize/thread-binding protocol and cached
// thread id are shared.
func SendTurn(ctx context.Context, sender Sender, text string, opts Options) error {
	if opts.Runtime == runtimes.ModeJSONRPCStdio {
		if rpc, ok := sender.(JSONRPCSender); ok {
			_, err := rpc.Call(ctx, method(opts), map[string]any{"message": text})
			return err
		}
	}
	frame, err := Frame(text, opts)
	if err != nil {
		return err
	}
	return sender.SendInput(ctx, frame)
}

type CodexAppServerOptions struct {
	ClientName    string
	ClientVersion string
	CWD           string
	// ResumeThreadID, when set, continues an existing Codex thread: the
	// session binds its thread with thread/resume instead of thread/start.
	// It is consulted only while the session has no thread bound yet, so a
	// caller can pass its stored provider session id on every turn.
	//
	// A resume that fails is returned as an error and never falls back to a
	// fresh thread, because a caller that asked to resume and got a new
	// thread would lose its history without being told. When Codex no
	// longer has the thread, the error is an *agentsessions.SessionLostError
	// (errors.Is(err, provider.ErrProviderSessionLost) holds). The session
	// stays unbound, so the next turn tries the resume again; to start over
	// on a new thread, Reset or Forget the session and clear this field.
	ResumeThreadID string
}

type CodexAppServerSession struct {
	mu          sync.Mutex
	threadID    string
	initialized bool
}

func (s *CodexAppServerSession) ThreadID() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threadID
}

func (s *CodexAppServerSession) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threadID = ""
	s.initialized = false
}

// SendTurn drives the Codex app-server JSON-RPC protocol: initialize once,
// bind a thread once (thread/resume when opts.ResumeThreadID is set,
// thread/start otherwise), cache thread.id, then turn/start for each user
// turn. Turn completion is reported by Codex notifications, not the
// turn/start response.
func (s *CodexAppServerSession) SendTurn(ctx context.Context, rpc JSONRPCSender, text string, opts CodexAppServerOptions) error {
	if s == nil {
		return errors.New("turn: nil codex app-server session")
	}
	if rpc == nil {
		return errors.New("turn: nil jsonrpc sender")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		if _, err := rpc.Call(ctx, "initialize", codexInitializeParams(opts)); err != nil {
			return fmt.Errorf("jsonrpc initialize: %w", err)
		}
		s.initialized = true
	}
	if s.threadID == "" {
		threadID, err := bindCodexThread(ctx, rpc, opts)
		if err != nil {
			return err
		}
		s.threadID = threadID
	}
	if _, err := rpc.Call(ctx, "turn/start", CodexTurnStartParams(s.threadID, text)); err != nil {
		return fmt.Errorf("jsonrpc turn/start: %w", err)
	}
	return nil
}

type CodexAppServerCache struct {
	m sync.Map
}

func (c *CodexAppServerCache) SendTurn(ctx context.Context, key string, rpc JSONRPCSender, text string, opts CodexAppServerOptions) error {
	if key == "" {
		return errors.New("turn: empty codex session key")
	}
	value, _ := c.m.LoadOrStore(key, &CodexAppServerSession{})
	session, _ := value.(*CodexAppServerSession)
	return session.SendTurn(ctx, rpc, text, opts)
}

func (c *CodexAppServerCache) ThreadID(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	value, ok := c.m.Load(key)
	if !ok {
		return "", false
	}
	session, _ := value.(*CodexAppServerSession)
	id := session.ThreadID()
	return id, id != ""
}

func (c *CodexAppServerCache) Forget(key string) {
	if key == "" {
		return
	}
	c.m.Delete(key)
}

// bindCodexThread resumes opts.ResumeThreadID when it is set and starts a new
// thread otherwise, returning the bound thread id.
func bindCodexThread(ctx context.Context, rpc JSONRPCSender, opts CodexAppServerOptions) (string, error) {
	if opts.ResumeThreadID == "" {
		res, err := rpc.Call(ctx, "thread/start", codexThreadStartParams(opts))
		if err != nil {
			return "", fmt.Errorf("jsonrpc thread/start: %w", err)
		}
		return DecodeCodexThreadID(res)
	}
	requested := opts.ResumeThreadID
	res, err := rpc.Call(ctx, "thread/resume", codexThreadResumeParams(opts))
	if err != nil {
		if isCodexThreadLost(err) {
			return "", &agentsessions.SessionLostError{RequestedID: requested, Err: fmt.Errorf("jsonrpc thread/resume: %w", err)}
		}
		return "", fmt.Errorf("jsonrpc thread/resume %q: %w", requested, err)
	}
	actual, err := DecodeCodexThreadID(res)
	if err != nil {
		return "", fmt.Errorf("jsonrpc thread/resume %q: %w", requested, err)
	}
	if actual != requested {
		// Codex answered with a different thread than the one asked for.
		// Binding it would continue in a thread without the requested
		// history, which is the silent fallback this path refuses.
		return "", &agentsessions.SessionLostError{RequestedID: requested, ActualID: actual, Err: ErrCodexThreadMismatch}
	}
	return actual, nil
}

// codexThreadLostMarkers are the thread/resume error messages that mean Codex
// does not have the requested thread, as codex-cli 0.159 words them:
// "no rollout found for thread id <id>" for a well-formed id with no stored
// rollout, and "invalid session id: ..." for an id Codex cannot parse, which
// no rollout can ever match. Any other thread/resume failure is returned as
// a plain error.
var codexThreadLostMarkers = []string{
	"no rollout found",
	"invalid session id",
}

func isCodexThreadLost(err error) bool {
	msg := err.Error()
	var rpcErr *agentsessions.JsonRpcError
	if errors.As(err, &rpcErr) {
		msg = rpcErr.Message
	}
	for _, marker := range codexThreadLostMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func DecodeCodexThreadID(raw json.RawMessage) (string, error) {
	var parsed struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("decode thread/start response: %w", err)
	}
	if parsed.Thread.ID != "" {
		return parsed.Thread.ID, nil
	}
	if parsed.ThreadID != "" {
		return parsed.ThreadID, nil
	}
	return "", ErrMissingThreadID
}

func CodexTurnStartParams(threadID, text string) map[string]any {
	return map[string]any{
		"threadId": threadID,
		"input": []map[string]any{
			{"type": "text", "text": text},
		},
	}
}

func codexInitializeParams(opts CodexAppServerOptions) map[string]any {
	name := opts.ClientName
	if name == "" {
		name = "go-agent-runtime"
	}
	version := opts.ClientVersion
	if version == "" {
		version = "unknown"
	}
	return map[string]any{
		"clientInfo": map[string]any{
			"name":    name,
			"version": version,
		},
	}
}

func codexThreadStartParams(opts CodexAppServerOptions) map[string]any {
	params := map[string]any{}
	if opts.CWD != "" {
		params["cwd"] = opts.CWD
	}
	return params
}

// codexThreadResumeParams asks for thread metadata only. Without
// excludeTurns the response carries the thread's whole history on one line,
// which for a long thread can outgrow the JSON-RPC stdio reader's line limit.
func codexThreadResumeParams(opts CodexAppServerOptions) map[string]any {
	params := map[string]any{
		"threadId":     opts.ResumeThreadID,
		"excludeTurns": true,
	}
	if opts.CWD != "" {
		params["cwd"] = opts.CWD
	}
	return params
}

// ClaudeStreamingUserFrame emits the exact NDJSON object Claude Code streaming
// stdio consumes. It deliberately returns no trailing newline; sessions appends
// the line break at write time. agentsessions owns the frame, so a prepared
// launch's boot turn and a consumer's turns are framed the same way.
func ClaudeStreamingUserFrame(text string) ([]byte, error) {
	return agentsessions.ClaudeStreamingUserFrame(text)
}

func method(opts Options) string {
	if opts.JSONRPCMethod != "" {
		return opts.JSONRPCMethod
	}
	return "turn.send"
}
