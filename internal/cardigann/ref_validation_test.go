package cardigann

import "testing"

func TestDefinitionRefRejectsUnsafeSourceAndIDCharacters(t *testing.T) {
	for _, value := range []string{
		"../user:fixture",
		"user:../fixture",
		"user:fixture/path",
		"User:fixture",
	} {
		if _, err := ParseDefinitionRef(value); err == nil {
			t.Errorf("ParseDefinitionRef(%q) accepted an unsafe reference", value)
		}
	}
}
