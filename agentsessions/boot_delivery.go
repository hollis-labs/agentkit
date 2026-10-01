package agentsessions

import (
	"encoding/json"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"

	"github.com/hollis-labs/agentkit/agentlaunch"
)

// ClaudeStreamingUserFrame is the NDJSON object Claude Code's streaming stdio
// reads as one user turn. It has no trailing newline; SendInput adds it.
// agentruntime/turn re-exports it.
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

// DeliverStreamingBoot gives a streaming-stdio launch its boot prompt. Claude
// in streaming-stdio mode takes every turn, the first included, as a
// stream-json frame on stdin, and a prepared launch's argv carries no prompt,
// so the boot prompt (BootContent, else BootPrompt) becomes the auto-fired
// first turn, framed. BootMode and BootPrompt are cleared so the session does
// not also write the unframed prompt to stdin.
//
// It does nothing for another mode, for BootMode "none", when there is no
// boot prompt, or when the caller already set AutoFireFirstTurn: a caller that
// fires its own first turn owns it. Start applies it to every launch with a
// template (StartOptions.Launch); the session shims apply it too, so their
// SessionLaunch already shows it.
func DeliverStreamingBoot(opts *StartOptions, mode runtimes.Mode) error {
	if opts == nil || mode != runtimes.ModeStreamingStdio || opts.BootMode == agentlaunch.BootModeNone || opts.AutoFireFirstTurn {
		return nil
	}
	text := opts.BootContent
	if text == "" {
		text = opts.BootPrompt
	}
	if text == "" {
		return nil
	}
	payload, err := ClaudeStreamingUserFrame(text)
	if err != nil {
		return err
	}
	opts.AutoFireFirstTurn = true
	opts.FirstTurnPayload = payload
	opts.BootMode = ""
	opts.BootPrompt = ""
	return nil
}
