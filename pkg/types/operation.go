package types

import "encoding/json"

// OpID is a unique identifier for an operation in the OpLog.
// Format: "op_<unixms>_<seq>"
type OpID string

func (id OpID) Valid() bool    { return id != "" }
func (id OpID) String() string { return string(id) }

// OpClass classifies an operation for storage and rollback semantics.
type OpClass string

const (
	OpClassContent  OpClass = "A" // stores content, rollbackable (config/small files)
	OpClassArtifact OpClass = "B" // stores metadata, deletable (downloads/install artifacts)
	OpClassState    OpClass = "C" // stores state, reversible (process/session/peer)
	OpClassNoop     OpClass = "D" // not recorded (read-only queries/streams)
)

// Operation is a single recorded operation in the append-only OpLog.
// It is the source of truth for all mutable state changes.
type Operation struct {
	OpID       OpID              `json:"opId"`
	Class      OpClass           `json:"class"`
	Capability string            `json:"capability"`
	Actor      Actor             `json:"actor"`
	Params     json.RawMessage   `json:"params"`               // the original request payload
	Timestamp  int64             `json:"timestamp"`             // unix millis
	SessionID  string            `json:"sessionId,omitempty"`   // associated session, if any
	TargetNode string            `json:"targetNode,omitempty"`  // target node for forwarded calls

	// A-class: content references (before/after)
	ContentBefore *ContentRef `json:"contentBefore,omitempty"`
	ContentAfter  *ContentRef `json:"contentAfter,omitempty"`

	// B-class: artifact tracking
	Artifacts []ArtifactRef `json:"artifacts,omitempty"`

	// C-class: state snapshots
	StateBefore *StateSnapshot `json:"stateBefore,omitempty"`
	StateAfter  *StateSnapshot `json:"stateAfter,omitempty"`

	// Rollback tracking
	RolledBackAt int64 `json:"rolledBackAt,omitempty"` // 0 = not rolled back
}

// ContentRef references content stored in the Content Store.
type ContentRef struct {
	Path     string `json:"path"`     // original filesystem path
	Hash     string `json:"hash"`     // SHA256 hex of content
	Size     int64  `json:"size"`     // content size in bytes
	StoredAt string `json:"storedAt"` // path within Content Store
}

// ArtifactRef references a file produced by a B-class operation.
type ArtifactRef struct {
	Path string `json:"path"`
	Hash string `json:"hash,omitempty"` // optional quick hash
	Size int64  `json:"size,omitempty"`
}

// StateSnapshot captures the state of an entity before or after an operation.
type StateSnapshot struct {
	Type     string      `json:"type"`     // "session", "run", "plan", "peer"
	StateID  string      `json:"stateId"`  // the entity's ID
	Snapshot interface{} `json:"snapshot"` // the state payload
}

// OpFilter filters operations in Query calls.
// All fields are AND-ed; zero values mean "no filter".
type OpFilter struct {
	Classes    []OpClass `json:"classes,omitempty"`
	Capability string    `json:"capability,omitempty"`
	ActorType  string    `json:"actorType,omitempty"`
	ActorID    string    `json:"actorId,omitempty"`
	TargetNode string    `json:"targetNode,omitempty"`
	SessionID  string    `json:"sessionId,omitempty"`
	FromTime   int64     `json:"fromTime,omitempty"` // inclusive, unix millis
	ToTime     int64     `json:"toTime,omitempty"`   // exclusive, unix millis
	Path       string    `json:"path,omitempty"`     // match against ContentRef.Path or ArtifactRef.Path
	Limit      int       `json:"limit,omitempty"`    // max results (0 = no limit)
	Offset     int       `json:"offset,omitempty"`   // skip first N results
}
