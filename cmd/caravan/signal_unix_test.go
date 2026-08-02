//go:build unix

package main

import (
	"os"
	"syscall"
	"testing"
)

// signalSelfTerm delivers SIGTERM to the test process to exercise the
// signal-shutdown path.
func signalSelfTerm(t *testing.T) {
	t.Helper()
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("signal caravan: %v", err)
	}
}

// signalProcessTerm delivers SIGTERM to a child process to exercise its
// clean-exit path.
func signalProcessTerm(t *testing.T, p *os.Process) {
	t.Helper()
	if err := p.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal caravan: %v", err)
	}
}
