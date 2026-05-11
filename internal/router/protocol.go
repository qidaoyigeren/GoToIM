package router

// ProtocolConverter translates between external business protobuf
// and the internal wire format (protocol.Proto).
// Phase 2: no-op pass-through. Phase 4: full external<->internal conversion.
type ProtocolConverter struct{}

// ExternalToInternal converts an external business protobuf to internal wire format.
// Stub: returns the raw bytes unchanged.
func (c *ProtocolConverter) ExternalToInternal(externalMsg []byte) ([]byte, error) {
	return externalMsg, nil
}

// InternalToExternal converts internal wire format to external business protobuf.
// Stub: returns the raw bytes unchanged.
func (c *ProtocolConverter) InternalToExternal(internal []byte) ([]byte, error) {
	return internal, nil
}
