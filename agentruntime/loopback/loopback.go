// Package loopback models provider-neutral MCP loopback descriptors.
package loopback

import "errors"

var (
	ErrInvalidDescriptor         = errors.New("loopback: invalid descriptor")
	ErrUnsupportedDescriptorKind = errors.New("loopback: unsupported descriptor kind")
)

type Kind string

const (
	Subprocess Kind = "subprocess"
	HTTPSSE    Kind = "http-sse"
	MuxProxy   Kind = "mux-proxy"
)

type Descriptor struct {
	Name      string
	Kind      Kind
	Command   string
	Args      []string
	URL       string
	Env       map[string]string
	Allowlist []string
	Metadata  map[string]string
}

func (d Descriptor) Validate() error {
	if d.Name == "" {
		return ErrInvalidDescriptor
	}
	switch d.Kind {
	case Subprocess, MuxProxy:
		if d.Command == "" {
			return ErrInvalidDescriptor
		}
	case HTTPSSE:
		if d.URL == "" {
			return ErrInvalidDescriptor
		}
	default:
		return ErrInvalidDescriptor
	}
	return nil
}
