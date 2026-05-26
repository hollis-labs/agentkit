// Package turn frames user turns before they are sent to go-agent-sessions.
package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	agentlaunch "github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentruntime/runtimekind"
)

var (
	ErrUnsupportedRuntime = errors.New("turn: unsupported runtime")
	ErrMissingThreadID    = errors.New("turn: missing codex thread id")
)

type Options struct {
	Provider string
	Runtime  agentlaunch.RuntimeKind
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
func Frame(text string, opts Options) ([]byte, error) {
	switch runtimekind.Parse(string(opts.Runtime)) {
	case runtimekind.StreamingStdio:
		return ClaudeStreamingUserFrame(text)
	case runtimekind.Subprocess, runtimekind.ServeHTTP, runtimekind.PTY, runtimekind.PTYDebug:
		return []byte(text), nil
	case runtimekind.API:
		return []byte(text), nil
	case runtimekind.JSONRPCStdio:
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
// CodexAppServerCache so the initialize/thread-start protocol and cached thread
// id are shared.
func SendTurn(ctx context.Context, sender Sender, text string, opts Options) error {
	if runtimekind.Parse(string(opts.Runtime)) == runtimekind.JSONRPCStdio {
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
// thread/start once, cache thread.id, then turn/start for each user turn.
// Turn completion is reported by Codex notifications, not the turn/start
// response.
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
		res, err := rpc.Call(ctx, "thread/start", codexThreadStartParams(opts))
		if err != nil {
			return fmt.Errorf("jsonrpc thread/start: %w", err)
		}
		threadID, err := DecodeCodexThreadID(res)
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

// ClaudeStreamingUserFrame emits the exact NDJSON object Claude Code streaming
// stdio consumes. It deliberately returns no trailing newline; sessions appends
// the line break at write time.
func ClaudeStreamingUserFrame(text string) ([]byte, error) {
	type userMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type frame struct {
		Type    string  `json:"type"`
		Message userMsg `json:"message"`
	}
	return json.Marshal(frame{Type: "user", Message: userMsg{Role: "user", Content: text}})
}

func method(opts Options) string {
	if opts.JSONRPCMethod != "" {
		return opts.JSONRPCMethod
	}
	return "turn.send"
}
