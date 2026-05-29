// Package config provides a layered configuration system for SessionNode Go Core.
//
// Config is loaded from a JSON file, with sensible defaults applied for any
// missing fields. The Manager provides thread-safe read/write access and
// supports dot-notation for nested field updates.
package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Config structs
// ---------------------------------------------------------------------------

// Config is the root configuration struct.
type Config struct {
	Core     CoreConfig     `json:"core"`
	Node     NodeConfig     `json:"node"`
	Plugin   PluginConfig   `json:"plugin"`
	Topology TopologyConfig `json:"topology,omitempty"`

	// Revision is an opaque counter incremented on every Save() or Set().
	// Used by SetWithRevision() for optimistic concurrency control.
	Revision int64 `json:"_revision"`
}

// TopologyConfig holds the peer discovery configuration.
type TopologyConfig struct {
	Peers []PeerConfig `json:"peers,omitempty"`
}

// PeerConfig defines a single peer node that this instance should connect to.
type PeerConfig struct {
	ID      string   `json:"id"`
	Address string   `json:"address"`        // "host:port"
	Tags    []string `json:"tags,omitempty"` // e.g. ["local"]
}

// CoreConfig holds server-level settings.
type CoreConfig struct {
	ListenAddr string     `json:"listenAddr"` // default ":9090"
	TLS        TLSConfig  `json:"tls,omitempty"`
	DataDir    string     `json:"dataDir"` // default "~/.sessionnode"
	Auth       AuthConfig `json:"auth"`
	Log        LogConfig  `json:"log"`
}

// TLSConfig holds optional TLS certificate paths.
type TLSConfig struct {
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
}

// AuthConfig controls admin authentication.
type AuthConfig struct {
	Enabled    bool   `json:"enabled"`
	AdminToken string `json:"adminToken,omitempty"`
}

// LogConfig controls log output behaviour.
type LogConfig struct {
	Level    string `json:"level"`    // "debug","info","warn","error"
	MaxSize  int    `json:"maxSize"`  // MB, default 100
	MaxFiles int    `json:"maxFiles"` // default 10
}

// NodeConfig identifies the local node.
type NodeConfig struct {
	Name string `json:"name"`
	Role string `json:"role"` // "standalone","relay","leaf"
}

// PluginConfig holds plugin-level settings.
type PluginConfig struct {
	PluginDirs      []string                              `json:"pluginDirs,omitempty"`
	DisabledPlugins []string                              `json:"disabledPlugins,omitempty"`
	Permissions     map[string]map[string]PermissionGrant `json:"permissions,omitempty"`
}

// PermissionGrant describes how a capability is gated for a plugin.
type PermissionGrant struct {
	Mode        string                 `json:"mode"` // "allow","deny","ask"
	Constraints map[string]interface{} `json:"constraints,omitempty"`
}

// ---------------------------------------------------------------------------
// Manager
// ---------------------------------------------------------------------------

// Manager provides thread-safe read/write access to a JSON-backed Config.
type Manager struct {
	mu     sync.RWMutex
	config Config
	path   string
}

// NewManager creates a Manager that reads from and writes to path.
func NewManager(path string) *Manager {
	return &Manager{
		path: path,
	}
}

// defaultConfig returns a Config with all defaults populated.  Fields that are
// not explicitly set receive zero values, which is fine for optional fields.
func defaultConfig() Config {
	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	dataDir := filepath.Join(home, ".sessionnode")

	return Config{
		Core: CoreConfig{
			ListenAddr: ":9090",
			DataDir:    dataDir,
			Log: LogConfig{
				Level:    "info",
				MaxSize:  100,
				MaxFiles: 10,
			},
		},
		Node: NodeConfig{
			Role: "standalone",
		},
		Plugin: PluginConfig{
			PluginDirs: []string{
				filepath.Join(dataDir, "plugins"),
			},
		},
	}
}

// applyDefaults sets default values on m.config for any zero-valued fields.
// It starts from a fresh default config and overlays any non-zero values from
// the current config on top.
func applyDefaults(cfg Config) Config {
	def := defaultConfig()

	// Walk top-level fields; if the current value is its zero value, use the
	// default.  Structs are handled recursively.
	setRoot := reflect.ValueOf(&cfg).Elem()
	setDef := reflect.ValueOf(def)

	applyStructDefaults(setRoot, setDef)
	return cfg
}

// applyStructDefaults recursively copies non-zero default fields into target.
func applyStructDefaults(target, defaults reflect.Value) {
	t := target.Type()
	for i := 0; i < t.NumField(); i++ {
		tf := target.Field(i)
		df := defaults.Field(i)

		switch tf.Kind() {
		case reflect.Struct:
			// If the entire target struct is zero, overwrite; otherwise recurse.
			if tf.IsZero() {
				tf.Set(df)
			} else {
				applyStructDefaults(tf, df)
			}
		default:
			// Scalars: only fill when target is the zero value.
			if tf.IsZero() && !df.IsZero() {
				tf.Set(df)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Load / Save
// ---------------------------------------------------------------------------

// Load reads the JSON config file at m.path.  If the file does not exist, a
// default config is created and written to disk.  Missing fields are filled
// with defaults.
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := os.Stat(m.path); os.IsNotExist(err) {
		log.Printf("[config] %s not found, creating with defaults", m.path)
		m.config = defaultConfig()
		return m.saveLocked()
	}

	data, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", m.path, err)
	}

	// Start with defaults, overlay what was read from disk.
	m.config = defaultConfig()
	if err := json.Unmarshal(data, &m.config); err != nil {
		return fmt.Errorf("unmarshal config %s: %w", m.path, err)
	}

	log.Printf("[config] loaded from %s", m.path)
	return nil
}

// ConfigConflictError is returned when SetWithRevision detects a concurrent write.
type ConfigConflictError struct {
	ExpectedRevision int64
	ActualRevision   int64
}

func (e *ConfigConflictError) Error() string {
	return fmt.Sprintf("config revision conflict: expected %d, actual %d", e.ExpectedRevision, e.ActualRevision)
}

// Save writes the current config to disk as pretty-printed JSON.
func (m *Manager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config.Revision++
	return m.saveLocked()
}

// SetWithRevision updates a config field only if the expectedRevision matches
// the current revision counter. Returns ConfigConflictError on mismatch.
// Pass expectedRevision=0 to bypass the check (first-time or force set).
func (m *Manager) SetWithRevision(key string, value interface{}, expectedRevision int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if expectedRevision != 0 && m.config.Revision != expectedRevision {
		return &ConfigConflictError{
			ExpectedRevision: expectedRevision,
			ActualRevision:   m.config.Revision,
		}
	}

	parts := strings.Split(key, ".")
	if len(parts) == 0 || parts[0] == "" {
		return fmt.Errorf("config: empty key")
	}

	v := reflect.ValueOf(&m.config).Elem()
	if err := setField(v, parts, value); err != nil {
		return fmt.Errorf("config: set %q: %w", key, err)
	}

	m.config.Revision++
	log.Printf("[config] %s = %v (rev %d)", key, value, m.config.Revision)
	m.config = applyDefaults(m.config)
	return m.saveLocked()
}

// Value returns a copy of a single config value addressed by dot notation.
func (m *Manager) Value(key string) (interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	parts := strings.Split(key, ".")
	if len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("config: empty key")
	}
	return getField(reflect.ValueOf(m.config), parts)
}

// ResetWithRevision resets a config key to its default value.
func (m *Manager) ResetWithRevision(key string, expectedRevision int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if expectedRevision != 0 && m.config.Revision != expectedRevision {
		return &ConfigConflictError{
			ExpectedRevision: expectedRevision,
			ActualRevision:   m.config.Revision,
		}
	}

	parts := strings.Split(key, ".")
	if len(parts) == 0 || parts[0] == "" {
		return fmt.Errorf("config: empty key")
	}

	value, err := getField(reflect.ValueOf(defaultConfig()), parts)
	if err != nil {
		return fmt.Errorf("config: reset %q: %w", key, err)
	}

	v := reflect.ValueOf(&m.config).Elem()
	if err := setField(v, parts, value); err != nil {
		return fmt.Errorf("config: reset %q: %w", key, err)
	}

	m.config.Revision++
	log.Printf("[config] reset %s (rev %d)", key, m.config.Revision)
	m.config = applyDefaults(m.config)
	return m.saveLocked()
}

// saveLocked writes m.config to m.path.  The caller must hold m.mu.
func (m *Manager) saveLocked() error {
	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(m.config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(m.path, data, 0644); err != nil {
		return fmt.Errorf("write config %s: %w", m.path, err)
	}
	return nil
}

func getField(v reflect.Value, parts []string) (interface{}, error) {
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil, fmt.Errorf("nil pointer at %q", parts[0])
		}
		v = v.Elem()
	}

	part := parts[0]
	rest := parts[1:]

	switch v.Kind() {
	case reflect.Struct:
		fv := fieldByJSONTag(v, part)
		if !fv.IsValid() {
			return nil, fmt.Errorf("unknown field %q", part)
		}
		if len(rest) == 0 {
			return fv.Interface(), nil
		}
		return getField(fv, rest)
	case reflect.Map:
		value := v.MapIndex(reflect.ValueOf(part))
		if !value.IsValid() {
			return nil, fmt.Errorf("unknown key %q", part)
		}
		if len(rest) == 0 {
			return value.Interface(), nil
		}
		return getField(value, rest)
	default:
		return nil, fmt.Errorf("cannot navigate into kind %s at %q", v.Kind(), part)
	}
}

// ---------------------------------------------------------------------------
// Read accessors
// ---------------------------------------------------------------------------

// Get returns a deep copy of the current config.  The copy is safe to mutate.
func (m *Manager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config // value copy — all fields are value types or nil maps
}

// PluginGrant returns the permission grant for (pluginID, capability), or nil
// if no grant is configured.
func (m *Manager) PluginGrant(pluginID, capability string) *PermissionGrant {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pm := m.config.Plugin.Permissions
	if pm == nil {
		return nil
	}
	inner, ok := pm[pluginID]
	if !ok {
		return nil
	}
	grant, ok := inner[capability]
	if !ok {
		return nil
	}
	// Return a copy so the caller cannot mutate the live config.
	g := grant
	return &g
}

// SetPermissionGrant sets the permission grant for (pluginID, capability).
// Unlike dot-notation Set, this handles capability names containing dots.
func (m *Manager) SetPermissionGrant(pluginID, capability, mode string, constraints map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.config.Plugin.Permissions == nil {
		m.config.Plugin.Permissions = make(map[string]map[string]PermissionGrant)
	}
	if m.config.Plugin.Permissions[pluginID] == nil {
		m.config.Plugin.Permissions[pluginID] = make(map[string]PermissionGrant)
	}
	m.config.Plugin.Permissions[pluginID][capability] = PermissionGrant{
		Mode:        mode,
		Constraints: constraints,
	}
	m.config.Revision++
	log.Printf("[config] permission grant: %s/%s = %s (rev %d)", pluginID, capability, mode, m.config.Revision)
	return m.saveLocked()
}

// RemovePermissionGrant removes a permission grant for (pluginID, capability).
func (m *Manager) RemovePermissionGrant(pluginID, capability string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.config.Plugin.Permissions != nil {
		if inner, ok := m.config.Plugin.Permissions[pluginID]; ok {
			delete(inner, capability)
			m.config.Revision++
			log.Printf("[config] permission revoke: %s/%s (rev %d)", pluginID, capability, m.config.Revision)
			return m.saveLocked()
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Dot-notation Set
// ---------------------------------------------------------------------------

// Set updates a config field using dot-notation.  Examples:
//
//	m.Set("core.listenAddr", ":7070")
//	m.Set("core.log.level", "debug")
//	m.Set("plugin.permissions.myPlugin.fs.mode", "allow")
//
// Map entries are created on demand.  The key is split on "." and each segment
// is resolved as a JSON tag name (for struct fields) or a map key (for maps).
func (m *Manager) Set(key string, value interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	parts := strings.Split(key, ".")
	if len(parts) == 0 || parts[0] == "" {
		return fmt.Errorf("config: empty key")
	}

	v := reflect.ValueOf(&m.config).Elem()
	if err := setField(v, parts, value); err != nil {
		return fmt.Errorf("config: set %q: %w", key, err)
	}

	m.config.Revision++
	log.Printf("[config] %s = %v (rev %d)", key, value, m.config.Revision)
	m.config = applyDefaults(m.config) // re-apply defaults after mutation
	return m.saveLocked()
}

// setField navigates into v following parts[0], then recurses with parts[1:].
// When parts is exhausted the value is written.
func setField(v reflect.Value, parts []string, value interface{}) error {
	// Dereference pointers until we hit a concrete kind.
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			if !v.CanSet() {
				return fmt.Errorf("cannot set nil pointer at %q", parts[0])
			}
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}

	part := parts[0]
	rest := parts[1:]

	switch v.Kind() {
	case reflect.Struct:
		fv := fieldByJSONTag(v, part)
		if !fv.IsValid() {
			return fmt.Errorf("unknown field %q", part)
		}
		if len(rest) == 0 {
			return setReflectValue(fv, value)
		}
		return setField(fv, rest, value)

	case reflect.Map:
		if v.IsNil() {
			if !v.CanSet() {
				return fmt.Errorf("cannot set nil map at %q", part)
			}
			v.Set(reflect.MakeMap(v.Type()))
		}

		keyVal := reflect.ValueOf(part)
		existing := v.MapIndex(keyVal)

		if len(rest) == 0 {
			// nil value removes the key from the map.
			if value == nil {
				v.SetMapIndex(keyVal, reflect.Value{})
				return nil
			}
			// Set the map value directly.
			val := reflect.ValueOf(value)
			if !val.Type().AssignableTo(v.Type().Elem()) {
				if val.Type().ConvertibleTo(v.Type().Elem()) {
					val = val.Convert(v.Type().Elem())
				} else {
					return fmt.Errorf(
						"cannot set map[%s]%s to %T",
						v.Type().Key(), v.Type().Elem(), value,
					)
				}
			}
			v.SetMapIndex(keyVal, val)
			return nil
		}

		// Navigate deeper: copy existing element (if any) or create a new one.
		elem := reflect.New(v.Type().Elem()).Elem()
		if existing.IsValid() {
			elem.Set(existing)
		}
		if err := setField(elem, rest, value); err != nil {
			return err
		}
		v.SetMapIndex(keyVal, elem)
		return nil

	default:
		return fmt.Errorf("cannot navigate into kind %s at %q", v.Kind(), part)
	}
}

// fieldByJSONTag returns the struct field in v whose `json` tag matches name,
// or an invalid Value if none is found.
func fieldByJSONTag(v reflect.Value, name string) reflect.Value {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)

		// Unexported fields cannot be set — skip them.
		if !f.IsExported() {
			continue
		}

		tag := f.Tag.Get("json")
		tagName := strings.Split(tag, ",")[0]

		if tagName == name {
			return v.Field(i)
		}
		// Fallback: if no json tag is set, use the Go field name.
		if tagName == "" && strings.EqualFold(f.Name, name) {
			return v.Field(i)
		}
	}
	return reflect.Value{}
}

// setReflectValue sets target to value, performing basic type conversions
// (float64 → int, string → int, etc.) where possible.
func setReflectValue(target reflect.Value, value interface{}) error {
	val := reflect.ValueOf(value)

	// Exact type match — fast path.
	if val.Type() == target.Type() {
		target.Set(val)
		return nil
	}

	// Cross-type conversions first — these handle cases where Go's built-in
	// Convert would produce surprising results (e.g. int → string producing a
	// single-rune string instead of a decimal representation).
	switch target.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		switch val.Kind() {
		case reflect.Float32, reflect.Float64:
			target.SetInt(int64(val.Float()))
			return nil
		case reflect.String:
			if n, ok := parseInt(val.String()); ok {
				target.SetInt(n)
				return nil
			}
		}

	case reflect.Float32, reflect.Float64:
		switch val.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			target.SetFloat(float64(val.Int()))
			return nil
		case reflect.String:
			if f, ok := parseFloat(val.String()); ok {
				target.SetFloat(f)
				return nil
			}
		}

	case reflect.String:
		switch val.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			target.SetString(fmt.Sprintf("%v", value))
			return nil
		case reflect.Float32, reflect.Float64:
			target.SetString(fmt.Sprintf("%v", value))
			return nil
		}

	case reflect.Bool:
		switch val.Kind() {
		case reflect.String:
			switch strings.ToLower(val.String()) {
			case "true", "1", "yes":
				target.SetBool(true)
				return nil
			case "false", "0", "no":
				target.SetBool(false)
				return nil
			}
		case reflect.Int, reflect.Int64:
			target.SetBool(val.Int() != 0)
			return nil
		}
	}

	// Handle []interface{} → typed slice (common when value comes from JSON decode).
	if val.Kind() == reflect.Slice && val.Type().Elem().Kind() == reflect.Interface {
		if target.Kind() == reflect.Slice {
			elemType := target.Type().Elem()
			slice := reflect.MakeSlice(target.Type(), 0, val.Len())
			for i := 0; i < val.Len(); i++ {
				elem := val.Index(i).Elem()
				if elem.Type().AssignableTo(elemType) {
					slice = reflect.Append(slice, elem)
				} else if elem.Type().ConvertibleTo(elemType) {
					slice = reflect.Append(slice, elem.Convert(elemType))
				} else {
					return fmt.Errorf("cannot set %s from %T: element %d is %T", target.Type(), value, i, elem.Interface())
				}
			}
			target.Set(slice)
			return nil
		}
	}

	// Fallback: standard Go conversion (e.g. int → int64, float32 → float64).
	if val.Type().ConvertibleTo(target.Type()) {
		target.Set(val.Convert(target.Type()))
		return nil
	}

	return fmt.Errorf("cannot set %s from %T", target.Type(), value)
}

// parseFloat parses a floating-point number from s.
func parseFloat(s string) (float64, bool) {
	if len(s) == 0 {
		return 0, false
	}
	var n float64
	var dec, div float64
	dec = -1
	for _, c := range s {
		if c == '.' && dec < 0 {
			dec = 0
			div = 10
			continue
		}
		if c < '0' || c > '9' {
			return 0, false
		}
		d := float64(c - '0')
		if dec >= 0 {
			n += d / div
			div *= 10
		} else {
			n = n*10 + d
		}
	}
	return n, true
}

// parseInt parses an integer from s.  Only base-10 values are accepted.
func parseInt(s string) (int64, bool) {
	if len(s) == 0 {
		return 0, false
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}
