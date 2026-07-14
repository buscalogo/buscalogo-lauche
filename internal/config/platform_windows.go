//go:build windows

package config

func platformDefaultCouchEnabled() bool { return false }

// Modo B Windows: CoreDNS :53 + NRPT (ver internal/dns/dns_windows.go).
func platformForceDNSLocal() bool { return false }
