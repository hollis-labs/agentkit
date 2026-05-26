// Package sessionkit projects shared runtime policy onto go-agent-sessions.
package sessionkit

import (
	"github.com/hollis-labs/agentkit/agentruntime/turn"
	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
)

type FirstTurnMode string

const (
	ManualFirstTurn        FirstTurnMode = "manual"
	AutoFireFirstTurn      FirstTurnMode = "auto-fire"
	ResumeWithoutFirstTurn FirstTurnMode = "resume-without-first-turn"
	OneShotPrompt          FirstTurnMode = "one-shot-prompt"
	BackgroundKickoff      FirstTurnMode = "background-kickoff"
)

type FirstTurnPolicy struct {
	Mode   FirstTurnMode
	Prompt string
	Turn   turn.Options
}

// ApplyFirstTurnPolicy mutates opts with the go-agent-sessions fields required
// for the selected policy. ResumeWithoutFirstTurn and ManualFirstTurn never
// auto-send, which prevents double-send on resume and attach-first flows.
func ApplyFirstTurnPolicy(opts *agentsessions.StartOptions, policy FirstTurnPolicy) error {
	if opts == nil {
		return nil
	}
	opts.AutoFireFirstTurn = false
	opts.FirstTurnPayload = nil
	switch policy.Mode {
	case "", ManualFirstTurn, ResumeWithoutFirstTurn:
		return nil
	case AutoFireFirstTurn, OneShotPrompt, BackgroundKickoff:
		payload, err := turn.Frame(policy.Prompt, policy.Turn)
		if err != nil {
			return err
		}
		opts.AutoFireFirstTurn = true
		opts.FirstTurnPayload = payload
		return nil
	default:
		return nil
	}
}
