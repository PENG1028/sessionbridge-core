package pluginmanifest

import (
	"fmt"
	"strconv"
	"strings"
)

// parseYAML converts a subset of YAML (maps, lists, strings, bools, ints)
// into a generic interface{} that can be round-tripped through JSON.
func parseYAML(data []byte) (interface{}, error) {
	lines := strings.Split(string(data), "\n")
	// Strip trailing empty line
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	root, _, err := parseBlock(lines, 0, 0)
	return root, err
}

// parseBlock parses a block of YAML starting at the given indentation level.
// Returns the parsed value, the number of lines consumed, and any error.
func parseBlock(lines []string, startIdx int, indent int) (interface{}, int, error) {
	if startIdx >= len(lines) {
		return nil, 0, nil
	}

	// Skip empty and comment-only lines
	idx := startIdx
	for idx < len(lines) {
		trimmed := strings.TrimSpace(lines[idx])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			idx++
			continue
		}
		break
	}
	if idx >= len(lines) {
		return nil, idx - startIdx, nil
	}

	// Check if this is a list item
	firstLine := lines[idx]
	content := firstLine
	if trimmed := strings.TrimSpace(firstLine); strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
		return parseList(lines, idx, indent)
	}

	// Check if it's a key: value pair
	colonIdx := findColon(content, indent)
	if colonIdx >= 0 {
		return parseMap(lines, idx, indent)
	}

	// Scalar value
	val, err := parseScalar(strings.TrimSpace(content))
	return val, 1, err
}

// parseMap parses a YAML mapping starting at the given line.
func parseMap(lines []string, startIdx int, baseIndent int) (map[string]interface{}, int, error) {
	result := make(map[string]interface{})
	idx := startIdx
	consumed := 0

	for idx < len(lines) {
		line := lines[idx]
		trimmed := strings.TrimSpace(line)

		// Skip empty/comment lines within map
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			idx++
			consumed++
			continue
		}

		// If line is less indented than baseIndent, we've left the map
		lineIndent := countIndent(line)
		if lineIndent < baseIndent && trimmed != "" {
			break
		}

		// Must be a key: value line
		colonIdx := findColon(line, 0)
		if colonIdx < 0 {
			return nil, consumed, fmt.Errorf("expected key: value at line %d: %s", idx+1, line)
		}

		key := strings.TrimSpace(line[baseIndent:colonIdx])
		if key == "" {
			return nil, consumed, fmt.Errorf("empty key at line %d", idx+1)
		}

		rest := strings.TrimSpace(line[colonIdx+1:])

		if rest == "" || strings.HasPrefix(rest, "#") {
			// Value starts on next indented line(s) — could be nested map or list
			nextIdx := idx + 1
			// Skip empty lines
			for nextIdx < len(lines) && strings.TrimSpace(lines[nextIdx]) == "" {
				nextIdx++
			}
			if nextIdx < len(lines) {
				nextIndent := countIndent(lines[nextIdx])
				if nextIndent > lineIndent {
					val, n, err := parseBlock(lines, nextIdx, nextIndent)
					if err != nil {
						return nil, consumed + n, err
					}
					result[key] = val
					idx = nextIdx + n
					consumed = idx - startIdx
					continue
				}
			}
			result[key] = nil
			idx++
			consumed++
			continue
		}

		// Check for comment after value
		if commentIdx := strings.Index(rest, " #"); commentIdx >= 0 {
			rest = strings.TrimSpace(rest[:commentIdx])
		}

		// Check for inline list or map before falling through to parseScalar
		if strings.HasPrefix(rest, "[") {
			result[key] = parseInlineList(rest)
			idx++
			consumed++
			continue
		}
		if strings.HasPrefix(rest, "{") {
			result[key] = parseInlineMap(rest)
			idx++
			consumed++
			continue
		}

		val, err := parseScalar(rest)
		if err != nil {
			val = rest
		}
		result[key] = val
		idx++
		consumed++
	}

	return result, consumed, nil
}

// parseList parses a YAML list.
func parseList(lines []string, startIdx int, baseIndent int) ([]interface{}, int, error) {
	var result []interface{}
	idx := startIdx
	consumed := 0

	for idx < len(lines) {
		line := lines[idx]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			idx++
			consumed++
			continue
		}

		lineIndent := countIndent(line)
		if lineIndent < baseIndent && trimmed != "" {
			break
		}

		if !strings.HasPrefix(trimmed, "- ") && trimmed != "-" {
			break
		}

		// Extract value after "- "
		itemStr := ""
		if strings.HasPrefix(trimmed, "- ") {
			itemStr = trimmed[2:]
		}

		if itemStr == "" || strings.HasPrefix(itemStr, "#") {
			// Item is on next indented line(s)
			nextIdx := idx + 1
			for nextIdx < len(lines) && strings.TrimSpace(lines[nextIdx]) == "" {
				nextIdx++
			}
			if nextIdx < len(lines) {
				nextIndent := countIndent(lines[nextIdx])
				itemIndent := lineIndent + 2 // typical indent for list items
				if nextIndent >= itemIndent {
					val, n, err := parseBlock(lines, nextIdx, nextIndent)
					if err != nil {
						return nil, consumed + n, err
					}
					result = append(result, val)
					idx = nextIdx + n
					consumed = idx - startIdx
					continue
				}
			}
			result = append(result, nil)
			idx++
			consumed++
			continue
		}

		// Check for comment after value
		if commentIdx := strings.Index(itemStr, " #"); commentIdx >= 0 {
			itemStr = strings.TrimSpace(itemStr[:commentIdx])
		}

		// Detect short-form map items: "- key: value" or "- key:"
		// This is the most common pattern for list items in plugin manifests.
		if ci := findColon(itemStr, 0); ci >= 0 && !strings.HasPrefix(itemStr[ci:], "://") {
			// Build synthetic lines at baseIndent for parseMap
			syntheticLines := []string{strings.Repeat(" ", baseIndent) + itemStr}
			nextIdx := idx + 1
			for nextIdx < len(lines) {
				nextLine := lines[nextIdx]
				trimmedNext := strings.TrimSpace(nextLine)
				if trimmedNext == "" || strings.HasPrefix(trimmedNext, "#") {
					syntheticLines = append(syntheticLines, nextLine)
					nextIdx++
					continue
				}
				nextIndent := countIndent(nextLine)
				if nextIndent <= baseIndent {
					break
				}
				syntheticLines = append(syntheticLines, nextLine)
				nextIdx++
			}
			val, _, err := parseMap(syntheticLines, 0, baseIndent)
			if err != nil {
				return nil, consumed + (nextIdx - startIdx), err
			}
			result = append(result, val)
			idx = nextIdx
			consumed = idx - startIdx
			continue
		}

		val, err := parseScalar(itemStr)
		if err != nil {
			val = itemStr
		}
		result = append(result, val)
		idx++
		consumed++
	}

	return result, consumed, nil
}

// parseScalar parses a YAML scalar value.
func parseScalar(s string) (interface{}, error) {
	if s == "" {
		return nil, fmt.Errorf("empty scalar")
	}

	// Quoted strings
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1], nil
		}
	}

	// Booleans
	switch strings.ToLower(s) {
	case "true", "yes", "on":
		return true, nil
	case "false", "no", "off":
		return false, nil
	case "null", "~":
		return nil, nil
	}

	// Integers
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i, nil
	}

	// Floats
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, nil
	}

	// Return as string
	return s, nil
}

// parseInlineList parses [a, b, c] into []interface{}.
func parseInlineList(s string) []interface{} {
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if inner == "" {
		return []interface{}{}
	}
	var result []interface{}
	for _, item := range strings.Split(inner, ",") {
		item = strings.TrimSpace(item)
		if v, err := parseScalar(item); err == nil {
			result = append(result, v)
		} else {
			result = append(result, item)
		}
	}
	return result
}

// parseInlineMap parses {k: v, k2: v2} into map[string]interface{}.
func parseInlineMap(s string) map[string]interface{} {
	result := make(map[string]interface{})
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if inner == "" {
		return result
	}
	for _, pair := range strings.Split(inner, ",") {
		pair = strings.TrimSpace(pair)
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val, _ := parseScalar(strings.TrimSpace(parts[1]))
			result[key] = val
		}
	}
	return result
}

// findColon finds the first colon that is not inside a quoted string.
func findColon(line string, startIdx int) int {
	inSingle := false
	inDouble := false
	for i := startIdx; i < len(line); i++ {
		ch := line[i]
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
		} else if ch == '"' && !inSingle {
			inDouble = !inDouble
		} else if ch == ':' && !inSingle && !inDouble {
			return i
		}
	}
	return -1
}

// countIndent counts leading spaces in a line.
func countIndent(line string) int {
	count := 0
	for _, ch := range line {
		if ch == ' ' {
			count++
		} else if ch == '\t' {
			count += 2 // treat tabs as 2 spaces
		} else {
			break
		}
	}
	return count
}

// yamlToJSON converts parsed YAML to JSON bytes for struct unmarshalling.
func yamlToJSON(data []byte) ([]byte, error) {
	parsed, err := parseYAML(data)
	if err != nil {
		return nil, fmt.Errorf("yaml parse error: %w", err)
	}
	return marshalJSON(parsed)
}
