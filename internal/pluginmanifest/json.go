package pluginmanifest

import "encoding/json"

// marshalJSON is a helper that marshals a value to JSON, normalizing types.
func marshalJSON(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}
