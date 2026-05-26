// Package checkpoint defines provider-session resume hints without imposing a
// shared database schema.
package checkpoint

import "time"

type ResumeSupport string

const (
	ResumeNative      ResumeSupport = "native"
	ResumeFreshBoot   ResumeSupport = "fresh-boot"
	ResumeUnsupported ResumeSupport = "unsupported"
)

type ResumeHint struct {
	Provider          string
	Runtime           string
	ProviderSessionID string
	Support           ResumeSupport
	FallbackFreshBoot bool
}

type Record struct {
	ID                string
	SessionID         string
	ProviderSessionID string
	Runtime           string
	CreatedAt         time.Time
	Metadata          map[string]string
}

func (h ResumeHint) CanResumeNatively() bool {
	return h.Support == ResumeNative && h.ProviderSessionID != ""
}

func (h ResumeHint) ShouldFreshBoot() bool {
	return h.Support == ResumeFreshBoot || (h.FallbackFreshBoot && !h.CanResumeNatively())
}
