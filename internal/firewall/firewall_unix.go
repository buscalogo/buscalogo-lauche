//go:build !windows

package firewall

import "buscalogo-agent/internal/logx"

// EnsureBLInboundRules é no-op fora do Windows.
func EnsureBLInboundRules(buf *logx.Buffer, libp2pPort, webPort, tlsPort int) error {
	return nil
}
