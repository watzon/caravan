package jsonpolicy

import (
	"strings"
	"testing"
)

// Regression: deeply nested input must be rejected, not recursed into, a 4 MiB
// manifest of '[' bytes previously overflowed the stack and killed the process
// before any signature check ran.
func TestValidateNoDuplicateKeysBoundsNestingDepth(t *testing.T) {
	deep := strings.Repeat("[", 4<<20)
	if err := ValidateNoDuplicateKeys([]byte(deep)); err == nil || !strings.Contains(err.Error(), "nesting exceeds") {
		t.Fatalf("deep nesting error = %v, want depth rejection", err)
	}
	okDepth := strings.Repeat("[", 63) + "1" + strings.Repeat("]", 63)
	if err := ValidateNoDuplicateKeys([]byte(okDepth)); err != nil {
		t.Fatalf("depth-63 value rejected: %v", err)
	}
}

func TestValidateNoDuplicateKeys(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "unique", input: `{"source":"community","nested":{"revision":"1"},"items":[{"id":"a"}]}`},
		{name: "duplicate top level", input: `{"source":"community","source":"evil"}`, wantErr: true},
		{name: "duplicate nested", input: `{"nested":{"revision":"1","revision":"2"}}`, wantErr: true},
		{name: "duplicate in array object", input: `{"items":[{"id":"a","id":"b"}]}`, wantErr: true},
		{name: "same key in separate objects", input: `[{"id":"a"},{"id":"b"}]`},
		{name: "multiple values", input: `{} {}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNoDuplicateKeys([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateNoDuplicateKeys() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
