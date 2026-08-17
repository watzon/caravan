// Package jsonpolicy contains fail-closed JSON validation shared by trusted
// configuration loaders.
package jsonpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// maxNestingDepth bounds recursion so untrusted input cannot exhaust the
// stack, matching the depth caps of the other bounded parsers (XML, torrent).
const maxNestingDepth = 64

// ValidateNoDuplicateKeys parses exactly one JSON value and rejects duplicate
// object keys at every nesting depth. The input bytes are never normalized, so
// callers may independently authenticate the exact original representation.
func ValidateNoDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := validateValue(decoder, 0); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return fmt.Errorf("read trailing JSON: %w", err)
		}
		return fmt.Errorf("unexpected trailing JSON token %v", token)
	}
	return nil
}

func validateValue(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("read JSON token: %w", err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if depth >= maxNestingDepth {
		return fmt.Errorf("JSON nesting exceeds %d levels", maxNestingDepth)
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("read JSON object key: %w", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[key] = struct{}{}
			if err := validateValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("close JSON object: %w", err)
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("invalid JSON object terminator %v", closing)
		}
	case '[':
		for decoder.More() {
			if err := validateValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("close JSON array: %w", err)
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("invalid JSON array terminator %v", closing)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
	return nil
}
