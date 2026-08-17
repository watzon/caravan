//go:build windows

package store

func setTestUmaskZero() func() { return func() {} }
