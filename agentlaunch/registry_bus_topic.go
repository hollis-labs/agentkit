package agentlaunch

import "fmt"

// registry_bus_topic.go adds the S3.5 bus-topic / state-stream registry
// kind. The Tether shared bus lets one app emit state and another
// subscribe; this kind makes those emitted state streams / bus topics
// discoverable as directory-registry entries so a consumer finds them
// through the directory rather than by out-of-band convention.
//
// Design constraints honored here:
//
//   - D2 (the directory holds resolver handles / descriptors, NOT
//     content). BusTopicContract describes WHERE a topic lives and HOW to
//     reach it — its address on the bus, the emit/subscribe direction, an
//     optional schema reference, and a transport hint. There is
//     deliberately no field that snapshots or carries the stream's actual
//     messages or payload data; the contract is a pure handle.
//
//   - D1 (local-first; pure data + validation, no network). The contract
//     and its Validate method perform structural checks only. Nothing here
//     connects to a running broker or message bus — wiring a live bus is
//     consumer-side integration and is out of scope for the registry kind.

// BusTopicDirection is the data-flow direction published for one bus
// topic contract: whether the registering app emits onto the topic,
// subscribes from it, or both.
type BusTopicDirection string

const (
	// BusTopicDirectionEmit marks a topic the registering app emits state
	// onto; consumers subscribe to it.
	BusTopicDirectionEmit BusTopicDirection = "emit"
	// BusTopicDirectionSubscribe marks a topic the registering app
	// consumes from; some other app emits onto it.
	BusTopicDirectionSubscribe BusTopicDirection = "subscribe"
	// BusTopicDirectionBidirectional marks a topic carrying traffic in
	// both directions.
	BusTopicDirectionBidirectional BusTopicDirection = "bidirectional"
)

// Valid reports whether d is a known bus-topic direction.
func (d BusTopicDirection) Valid() bool {
	switch d {
	case BusTopicDirectionEmit, BusTopicDirectionSubscribe, BusTopicDirectionBidirectional:
		return true
	default:
		return false
	}
}

// BusTopicContract publishes one shared-bus state stream / topic as a
// registry entry. It is a discovery handle only: it describes the
// topic's address on the bus and how to reach it, never the stream's
// messages. The directory copy is optional enrichment; the contract is
// safe to register and resolve locally with no broker connection.
type BusTopicContract struct {
	Meta RegistryContractMeta `yaml:"meta" json:"meta"`
	// Topic is the topic name / address on the shared bus. It is the
	// stable handle a consumer resolves to subscribe or emit.
	Topic string `yaml:"topic" json:"topic"`
	// Direction is the data-flow stance of the registering app relative
	// to this topic (emit / subscribe / bidirectional).
	Direction BusTopicDirection `yaml:"direction" json:"direction"`
	// SchemaRef optionally references a registered contract object that
	// describes the topic's payload / state schema. It is a handle to a
	// schema descriptor, never inline payload data.
	SchemaRef *RegistryObjectRef `yaml:"schema_ref,omitempty" json:"schema_ref,omitempty"`
	// Transport is an optional hint naming the bus transport family
	// (for example "tether-bus", "nats", "in-process"). It is advisory
	// metadata for consumers; it carries no connection credentials.
	Transport string `yaml:"transport,omitempty" json:"transport,omitempty"`
}

func (c BusTopicContract) RegistryMeta() RegistryContractMeta { return c.Meta }

// Validate enforces the bus-topic handle contract: the common contract
// metadata, a non-empty topic address, a known direction, and — when
// present — a well-formed schema reference that targets a contract
// object. It performs structural checks only and never touches a bus.
func (c BusTopicContract) Validate() error {
	if err := c.Meta.Validate(RegistryKindBusTopic, BusTopicSchemaVersionV1, BusTopicInterfaceV1); err != nil {
		return err
	}
	if c.Topic == "" {
		return ErrRegistryBusTopicMissingTopic
	}
	if !c.Direction.Valid() {
		return ErrRegistryBusTopicUnknownDirection
	}
	if c.SchemaRef != nil {
		if err := c.SchemaRef.Validate(); err != nil {
			return err
		}
		if c.SchemaRef.Kind != RegistryKindContractObject {
			return fmt.Errorf("%w: bus-topic schema_ref must target %q", ErrRegistryUnknownKind, RegistryKindContractObject)
		}
	}
	return nil
}

// compile-time assertion that BusTopicContract satisfies RegistryContract.
var _ RegistryContract = BusTopicContract{}
