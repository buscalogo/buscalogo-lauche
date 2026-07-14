//go:build !windows

package config

func platformDefaultCouchEnabled() bool { return true }
func platformForceDNSLocal() bool       { return false }
