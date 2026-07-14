//go:build windows

package firewall

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"buscalogo-agent/internal/logx"
)

const ruleName = "BuscaLogo Ygg"

// EnsureBLInboundRules cria regra inbound TCP para gossip/discover/HTTP/HTTPS se ainda não existir.
func EnsureBLInboundRules(buf *logx.Buffer, libp2pPort, webPort, tlsPort int) error {
	if libp2pPort <= 0 {
		libp2pPort = 4401
	}
	if webPort <= 0 {
		webPort = 80
	}
	if tlsPort <= 0 {
		tlsPort = 443
	}
	ports := []int{libp2pPort, libp2pPort + 1, webPort, tlsPort}
	portList := make([]string, 0, len(ports))
	for _, p := range ports {
		portList = append(portList, strconv.Itoa(p))
	}
	joined := strings.Join(portList, ",")

	check := exec.Command("powershell", "-NoProfile", "-Command",
		fmt.Sprintf(`if (Get-NetFirewallRule -DisplayName '%s' -ErrorAction SilentlyContinue) { 'EXISTS' }`, ruleName))
	out, _ := check.CombinedOutput()
	if strings.Contains(string(out), "EXISTS") {
		if buf != nil {
			buf.Infof("firewall", "regra %q já existe", ruleName)
		}
		return nil
	}

	ps := fmt.Sprintf(
		`New-NetFirewallRule -DisplayName '%s' -Direction Inbound -Action Allow -Protocol TCP -LocalPort %s -ErrorAction Stop`,
		ruleName, joined,
	)
	cmd := exec.Command("powershell", "-NoProfile", "-Command", ps)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("criar regra firewall: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	if buf != nil {
		buf.Infof("firewall", "regra %q criada (TCP %s)", ruleName, joined)
	}
	return nil
}
