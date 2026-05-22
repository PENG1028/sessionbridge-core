package executor

import (
	"encoding/json"
	"time"
)

// decodePayload unmarshals a json.RawMessage into the given target.
// Returns nil error if payload is empty (no-op).
func decodePayload(payload []byte, target interface{}) error {
	if len(payload) == 0 {
		return nil
	}
	return json.Unmarshal(payload, target)
}

func fmtTimestamp(ts int64) string {
	return time.UnixMilli(ts).UTC().Format(time.RFC3339)
}
