//go:build windows

package main

import (
	"os"
	"testing"
)

// signalSelfTerm has no Windows equivalent: there is no way to deliver the
// SIGTERM the signal-shutdown path listens for to our own process, so the
// step that depends on it is skipped there.
func signalSelfTerm(t *testing.T) {
	t.Helper()
	t.Skip("SIGTERM cannot be self-delivered on Windows; signal-path step covered on unix")
}

// signalProcessTerm: sending SIGTERM to a child is likewise unsupported on
// Windows, so steps that depend on it are skipped there.
func signalProcessTerm(t *testing.T, _ *os.Process) {
	t.Helper()
	t.Skip("SIGTERM cannot be delivered to a child on Windows; clean-exit step covered on unix")
}
