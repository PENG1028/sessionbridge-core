package protocol

import (
	"encoding/json"

	"github.com/user/sessionnode/go-core/pkg/types"
)

// MarshalJSON serializes the message. Exists as an explicit method to allow
// future hook points (envelope encryption, compression, etc.).
func (m *Message) MarshalJSON() ([]byte, error) {
	// Use a type alias to avoid infinite recursion when json.Marshal calls MarshalJSON.
	type Alias Message
	return json.Marshal((*Alias)(m))
}

// UnmarshalMessage deserializes a JSON byte slice into a Message.
func UnmarshalMessage(data []byte) (*Message, error) {
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// DecodePayload unmarshals the Payload field into the provided value.
// Returns nil if Payload is nil or empty.
func (m *Message) DecodePayload(v interface{}) error {
	if len(m.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(m.Payload, v)
}

// NewCoreError creates a CoreError with the given code and message.
func NewCoreError(code, message string) *types.CoreError {
	return &types.CoreError{Code: code, Message: message}
}
