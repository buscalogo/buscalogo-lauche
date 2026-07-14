//go:build windows

package dns

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/system"
)

const nrptDisplayPrefix = "BuscaLogo"

// Detect — Windows: porta 53, elevação Admin, regras NRPT (.bl → 127.0.0.1).
func Detect(cfg *config.Config) SystemInfo {
	info := SystemInfo{
		Platform:           "windows",
		CurrentMode:        cfg.DNS.Mode,
		SetcapAvailable:    false,
		AdminAvailable:     system.IsRoot(),
		ResolvConfMode:     "n/a",
		Port53Listeners:    []string{},
		Port53LoopbackFree: port53Free(),
	}
	if !info.Port53LoopbackFree {
		info.Port53Listeners = []string{"127.0.0.1:53"}
	}
	info.NrptConfigured = nrptConfigured(cfg)
	info.UsesBuscaLogoDNS = info.NrptConfigured && cfg.DNS.Mode == "system"
	return info
}

func port53Free() bool {
	// UDP :53 é o que o DNS do Windows usa.
	c, err := net.ListenPacket("udp4", "127.0.0.1:53")
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func searchNamespaces(cfg *config.Config) []string {
	tlds := cfg.DNS.SearchDomains
	if len(tlds) == 0 {
		tlds = []string{"bl"}
	}
	var out []string
	for _, t := range tlds {
		t = strings.TrimSpace(strings.TrimPrefix(t, "."))
		if t == "" {
			continue
		}
		out = append(out, "."+t)
	}
	return out
}

func nrptDisplayName(namespace string) string {
	return nrptDisplayPrefix + strings.TrimPrefix(namespace, ".")
}

func nrptConfigured(cfg *config.Config) bool {
	for _, ns := range searchNamespaces(cfg) {
		ok, err := nrptHas(ns)
		if err != nil || !ok {
			return false
		}
	}
	return len(searchNamespaces(cfg)) > 0
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func runPowerShell(script string) (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func nrptHas(namespace string) (bool, error) {
	name := nrptDisplayName(namespace)
	script := fmt.Sprintf(`
$ns = %s
$name = %s
$r = Get-DnsClientNrptRule -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -eq $name -or $_.Namespace -eq $ns }
if ($r) { 'yes' } else { 'no' }
`, psQuote(namespace), psQuote(name))
	out, err := runPowerShell(script)
	if err != nil {
		return false, fmt.Errorf("NRPT consulta: %w (%s)", err, out)
	}
	return strings.Contains(out, "yes"), nil
}

func nrptEnsure(namespace string) error {
	name := nrptDisplayName(namespace)
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$ns = %s
$name = %s
$existing = Get-DnsClientNrptRule -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -eq $name -or $_.Namespace -eq $ns }
if ($existing) {
  $existing | Remove-DnsClientNrptRule -Force -ErrorAction SilentlyContinue
}
Add-DnsClientNrptRule -Namespace $ns -NameServers '127.0.0.1' -DisplayName $name
'ok'
`, psQuote(namespace), psQuote(name))
	out, err := runPowerShell(script)
	if err != nil {
		return fmt.Errorf("NRPT Add %s: %w (%s)", namespace, err, out)
	}
	return nil
}

func nrptRemoveAll() error {
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Continue'
Get-DnsClientNrptRule -ErrorAction SilentlyContinue |
  Where-Object { $_.DisplayName -like %s } |
  ForEach-Object { Remove-DnsClientNrptRule -Name $_.Name -Force -ErrorAction SilentlyContinue }
'ok'
`, psQuote(nrptDisplayPrefix+"*"))
	out, err := runPowerShell(script)
	if err != nil {
		return fmt.Errorf("NRPT remove: %w (%s)", err, out)
	}
	return nil
}

// EnableSystem — Modo B Windows: CoreDNS 127.0.0.1:53 + NRPT .bl → 127.0.0.1.
func (m *Manager) EnableSystem() error {
	if !system.IsRoot() {
		return fmt.Errorf("execute o BuscaLogo Agent como Administrador para ativar DNS no sistema (porta 53 + NRPT)")
	}
	info := Detect(m.cfg)
	if !info.Port53LoopbackFree && m.cfg.DNS.Mode != "system" {
		return fmt.Errorf("127.0.0.1:53 ocupada — feche outro DNS/recursor ou libere a porta")
	}

	m.cfg.DNS.Mode = "system"
	m.cfg.DNS.Port = 53
	if err := m.cfg.Save(); err != nil {
		return err
	}
	if err := m.coredns.Restart(); err != nil {
		return fmt.Errorf("reiniciar coredns em :53: %w", err)
	}
	// Espera CoreDNS assumir :53 antes do NRPT.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !port53Free() {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	for _, ns := range searchNamespaces(m.cfg) {
		if err := nrptEnsure(ns); err != nil {
			return err
		}
		m.buf.Infof("dns", "NRPT %s → 127.0.0.1", ns)
	}
	m.buf.Infof("dns", "Modo B ativo (Windows): CoreDNS :53 + NRPT .bl")
	return nil
}

// DisableSystem — remove NRPT e volta CoreDNS a :5333.
func (m *Manager) DisableSystem() error {
	if err := nrptRemoveAll(); err != nil {
		m.buf.Warnf("dns", "remoção NRPT: %v", err)
	} else {
		m.buf.Infof("dns", "regras NRPT BuscaLogo removidas")
	}

	m.cfg.DNS.Mode = "local"
	m.cfg.DNS.Port = 5333
	if err := m.cfg.Save(); err != nil {
		return err
	}
	if err := m.coredns.Restart(); err != nil {
		return fmt.Errorf("reiniciar coredns em :5333: %w", err)
	}
	m.buf.Infof("dns", "Modo A ativo: coredns em 127.0.0.1:5333")
	return nil
}

// EnsureSystemIntegration re-aplica NRPT se a config já está em Modo B (pós-reboot).
func (m *Manager) EnsureSystemIntegration() {
	if m == nil || m.cfg == nil || m.cfg.DNS.Mode != "system" {
		return
	}
	if !system.IsRoot() {
		m.buf.Warnf("dns", "Modo B na config mas Agent sem Admin — NRPT/.bl pode falhar; rode como Administrador")
		return
	}
	for _, ns := range searchNamespaces(m.cfg) {
		ok, err := nrptHas(ns)
		if err != nil {
			m.buf.Warnf("dns", "NRPT check %s: %v", ns, err)
			continue
		}
		if ok {
			continue
		}
		if err := nrptEnsure(ns); err != nil {
			m.buf.Warnf("dns", "reaplicar NRPT %s: %v", ns, err)
			continue
		}
		m.buf.Infof("dns", "NRPT %s reaplicado", ns)
	}
}
