package dns

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/paths"
	"buscalogo-agent/internal/system"
)

type SystemInfo struct {
	HasSystemdResolved bool     `json:"has_systemd_resolved"`
	ResolvedStubActive bool     `json:"resolved_stub_active"`
	HasNetworkManager  bool     `json:"has_network_manager"`
	ResolvConfMode     string   `json:"resolv_conf_mode"` // stub | foreign
	Port53LoopbackFree bool     `json:"port_53_loopback_free"`
	Port53Listeners    []string `json:"port_53_listeners"`
	SetcapAvailable    bool     `json:"setcap_available"`
	CurrentMode        string   `json:"current_mode"`
	UsesBuscaLogoDNS   bool     `json:"uses_buscalogo_dns"`
}

// Detect coleta informações (somente leitura) sobre o resolvedor do host.
func Detect(cfg *config.Config) SystemInfo {
	info := SystemInfo{
		CurrentMode:     cfg.DNS.Mode,
		SetcapAvailable: system.HasCommand("setcap"),
		Port53Listeners: []string{},
	}

	if isActive("systemd-resolved") {
		info.HasSystemdResolved = true
	}
	if isActive("NetworkManager") {
		info.HasNetworkManager = true
	}

	mode, stub := resolvConfMode()
	info.ResolvConfMode = mode
	info.ResolvedStubActive = stub

	listeners, loopFree := scanPort53()
	info.Port53Listeners = listeners
	info.Port53LoopbackFree = loopFree

	if rc, _ := os.ReadFile("/etc/resolv.conf"); bytes.Contains(rc, []byte("nameserver 127.0.0.1")) {
		info.UsesBuscaLogoDNS = true
	}
	return info
}

func isActive(unit string) bool {
	out, err := exec.Command("systemctl", "is-active", "--quiet", unit).CombinedOutput()
	_ = out
	return err == nil
}

func resolvConfMode() (mode string, stubActive bool) {
	target, err := os.Readlink("/etc/resolv.conf")
	if err == nil {
		if strings.Contains(target, "systemd") || strings.Contains(target, "run/systemd") {
			return "stub", true
		}
		return "symlink", false
	}
	return "foreign", false
}

func scanPort53() (listeners []string, loopbackFree bool) {
	out, err := exec.Command("ss", "-H", "-tuln").CombinedOutput()
	if err != nil {
		return nil, true
	}
	loopbackFree = true
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		for _, f := range fields {
			if hasPort(f, 53) {
				listeners = append(listeners, f)
				if strings.HasPrefix(f, "127.0.0.1:53") {
					loopbackFree = false
				}
			}
		}
	}
	if listeners == nil {
		listeners = []string{}
	}
	return listeners, loopbackFree
}

func hasPort(addr string, port int) bool {
	return strings.HasSuffix(addr, fmt.Sprintf(":%d", port)) || strings.HasSuffix(addr, fmt.Sprintf(":%d%%lo", port))
}

// Manager controla a troca entre Modo A (local) e Modo B (system) de forma reversível.
type Manager struct {
	cfg     *config.Config
	buf     *logx.Buffer
	coredns CorednsController
}

type CorednsController interface {
	BinaryPath() (string, error)
	Restart() error
}

func NewManager(cfg *config.Config, buf *logx.Buffer, cdns CorednsController) *Manager {
	return &Manager{cfg: cfg, buf: buf, coredns: cdns}
}

// EnableSystem aplica o Modo B: capability + bind :53 + integração com o resolvedor.
func (m *Manager) EnableSystem() error {
	info := Detect(m.cfg)
	if !info.SetcapAvailable {
		return fmt.Errorf("setcap indisponível neste sistema; não é possível habilitar DNS em :53 sem root permanente")
	}

	binary, err := m.coredns.BinaryPath()
	if err != nil {
		return err
	}

	var logbuf bytes.Buffer
	w := ioMulti(&logbuf, m.buf)
	if !system.HasCapContains(binary, "cap_net_bind_service") {
		if err := system.SetCapNetBindService(w, binary); err != nil {
			return err
		}
		m.buf.Infof("dns", "capability concedida ao coredns")
	} else {
		m.buf.Infof("dns", "coredns já possui cap_net_bind_service")
	}

	m.cfg.DNS.Mode = "system"
	m.cfg.DNS.Port = 53
	if err := m.cfg.Save(); err != nil {
		return err
	}

	if err := m.coredns.Restart(); err != nil {
		return fmt.Errorf("reiniciar coredns em :53: %w", err)
	}

	if err := m.integrateResolver(w, info); err != nil {
		m.buf.Errorf("dns", "integração do resolvedor falhou (coredns já está em :53): %v", err)
		return err
	}
	m.buf.Infof("dns", "Modo B ativo: coredns em 127.0.0.1:53 + resolvedor integrado")
	return nil
}

// DisableSystem reverte para o Modo A.
func (m *Manager) DisableSystem() error {
	var logbuf bytes.Buffer
	w := ioMulti(&logbuf, m.buf)

	if err := m.revertResolver(w); err != nil {
		m.buf.Warnf("dns", "reversão parcial do resolvedor: %v", err)
	}

	binary, err := m.coredns.BinaryPath()
	if err == nil {
		if err := system.ClearCap(w, binary); err != nil {
			m.buf.Warnf("dns", "limpar capability: %v", err)
		}
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

func (m *Manager) integrateResolver(w interface{ Write([]byte) (int, error) }, info SystemInfo) error {
	tlds := m.cfg.DNS.SearchDomains
	if len(tlds) == 0 {
		tlds = []string{"bl"}
	}
	var domains []string
	for _, t := range tlds {
		t = strings.TrimPrefix(t, ".")
		domains = append(domains, "~"+t)
	}

	// Só usa drop-in do systemd-resolved quando ele realmente gerencia o DNS
	// (resolv.conf é symlink para o stub 127.0.0.53). Nesse caso não tocamos
	// no resolv.conf — as aplicações já consultam o systemd-resolved via stub.
	if info.HasSystemdResolved && info.ResolvedStubActive {
		if err := m.writeResolvedDropin(domains, w); err != nil {
			return fmt.Errorf("drop-in systemd-resolved: %w", err)
		}
		if _, err := system.RunPrivileged(w, "resolvectl", "reload"); err != nil {
			m.buf.Warnf("dns", "reload systemd-resolved: %v", err)
		}
		m.buf.Infof("dns", "systemd-resolved configurado via drop-in (resolv.conf preservado)")
		return nil
	}

	// Sem systemd-resolved gerenciando o DNS: modifica /etc/resolv.conf
	// adicionando 127.0.0.1 como primeiro nameserver.
	return m.replaceResolvConf(w)
}

func (m *Manager) writeResolvedDropin(domains []string, w interface{ Write([]byte) (int, error) }) error {
	confDir := "/etc/systemd/resolved.conf.d"
	confPath := filepath.Join(confDir, "99-buscalogo.conf")

	// Tenta detectar DNS atuais; se falhar (já estamos com 127.0.0.1), usa o backup.
	existing := m.currentResolvedDNS()
	if len(existing) == 0 || (len(existing) == 1 && existing[0] == "127.0.0.1") {
		existing = m.loadOriginalDNS()
	}
	// Salva os DNS originais na primeira vez que detectamos algo útil.
	if len(existing) > 0 {
		m.saveOriginalDNS(existing)
	}
	dnsLine := "DNS=127.0.0.1"
	for _, s := range existing {
		if s != "127.0.0.1" {
			dnsLine += " " + s
		}
	}

	content := "# BuscaLogo Agent — Modo B (CoreDNS em 127.0.0.1:53)\n" +
		"[Resolve]\n" +
		dnsLine + "\n" +
		"Domains=" + strings.Join(domains, " ") + "\n"

	tmp, err := os.CreateTemp("", "bl-resolved-*.conf")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if _, err := system.RunPrivileged(w, "mkdir", "-p", confDir); err != nil {
		return err
	}
	if _, err := system.RunPrivileged(w, "cp", tmp.Name(), confPath); err != nil {
		return err
	}
	m.buf.Infof("dns", "drop-in systemd-resolved: 127.0.0.1 + %d fallbacks preservados", len(existing))
	return nil
}

// currentResolvedDNS consulta os servidores DNS atuais do systemd-resolved.
func (m *Manager) currentResolvedDNS() []string {
	out, err := exec.Command("resolvectl", "dns").CombinedOutput()
	if err != nil {
		return nil
	}
	// Saída típica: "Global: 1.1.1.1 8.8.8.8\n..."
	line := strings.TrimSpace(string(out))
	_, after, ok := strings.Cut(line, ":")
	if !ok {
		return nil
	}
	var servers []string
	for _, s := range strings.Fields(after) {
		s = strings.TrimSpace(s)
		if s != "" {
			servers = append(servers, s)
		}
	}
	return servers
}

func (m *Manager) removeResolvedDropin(w interface{ Write([]byte) (int, error) }) error {
	confPath := "/etc/systemd/resolved.conf.d/99-buscalogo.conf"
	if _, err := os.Stat(confPath); err == nil {
		if _, err := system.RunPrivileged(w, "rm", "-f", confPath); err != nil {
			return err
		}
		m.buf.Infof("dns", "drop-in persistente do systemd-resolved removido")
	}
	return nil
}

func (m *Manager) replaceResolvConf(w interface{ Write([]byte) (int, error) }) error {
	backupDir, err := paths.Data()
	if err != nil {
		return err
	}
	backupPath := filepath.Join(backupDir, "resolv.conf.backup")

	current, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return fmt.Errorf("ler /etc/resolv.conf: %w", err)
	}

	// Se já tem BuscaLogo, restaura o backup primeiro (se existir) para readicionar.
	if bytes.Contains(current, []byte("BuscaLogo Agent")) {
		if backupData, err := os.ReadFile(backupPath); err == nil {
			current = backupData
			m.buf.Infof("dns", "restaurado backup antes de readicionar 127.0.0.1")
		} else {
			m.buf.Infof("dns", "/etc/resolv.conf já configurado com BuscaLogo (sem backup)")
			// Ainda assim tenta preservar o que tem — já está com 127.0.0.1.
			return nil
		}
	}

	// Salva backup do conteúdo ORIGINAL apenas na primeira vez (se ainda não salvou).
	if _, err := os.Stat(backupPath); err != nil {
		if err := os.WriteFile(backupPath, current, 0o644); err != nil {
			return fmt.Errorf("salvar backup: %w", err)
		}
		m.buf.Infof("dns", "backup de /etc/resolv.conf em %s", backupPath)
	}

	// Salva os nameservers originais como fallback para o drop-in do resolved.
	nameservers := extractNameservers(string(current))
	m.saveOriginalDNS(nameservers)

	// Prepend nameserver 127.0.0.1 mantendo TODAS as linhas originais.
	newContent := "# BuscaLogo Agent — CoreDNS em 127.0.0.1:53 (primeiro)\n" +
		"nameserver 127.0.0.1\n" +
		"options edns0 trust-ad\n"
	for _, line := range strings.Split(string(current), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "nameserver 127.0.0.1" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		newContent += line + "\n"
	}

	tmp, err := os.CreateTemp("", "bl-resolv-*.conf")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(newContent); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if _, err := system.RunPrivileged(w, "cp", tmp.Name(), "/etc/resolv.conf"); err != nil {
		return fmt.Errorf("aplicar novo resolv.conf: %w", err)
	}
	m.buf.Infof("dns", "/etc/resolv.conf: 127.0.0.1 adicionado como primeiro DNS, originais preservados como fallback")
	return nil
}

func (m *Manager) revertResolver(w interface{ Write([]byte) (int, error) }) error {
	// Remove drop-in persistente do systemd-resolved (se existir).
	if err := m.removeResolvedDropin(w); err != nil {
		m.buf.Warnf("dns", "remover drop-in persistente: %v", err)
	}

	_, stubActive := resolvConfMode()
	if isActive("systemd-resolved") && stubActive {
		// systemd-resolved gerencia o DNS: só recarregar para reverter ao config original.
		if _, err := system.RunPrivileged(w, "resolvectl", "revert", "lo"); err != nil {
			m.buf.Warnf("dns", "resolvectl revert: %v", err)
		}
		if _, err := system.RunPrivileged(w, "resolvectl", "reload"); err != nil {
			m.buf.Warnf("dns", "resolvectl reload: %v", err)
		}
		m.buf.Infof("dns", "systemd-resolved restaurado via revert/reload")
		return nil
	}

	// Sem systemd-resolved gerenciando: restaura /etc/resolv.conf do backup.
	backupDir, err := paths.Data()
	if err != nil {
		return err
	}
	backupPath := filepath.Join(backupDir, "resolv.conf.backup")
	if _, err := os.Stat(backupPath); err == nil {
		if _, err := system.RunPrivileged(w, "cp", backupPath, "/etc/resolv.conf"); err != nil {
			return fmt.Errorf("restaurar resolv.conf: %w", err)
		}
		os.Remove(backupPath)
		m.buf.Infof("dns", "/etc/resolv.conf restaurado do backup")
		return nil
	}

	return fmt.Errorf("sem systemd-resolved gerenciando e sem backup de /etc/resolv.conf — DNS pode precisar de reparo manual")
}

// saveOriginalDNS salva os servidores DNS originais num arquivo (uma vez apenas).
func (m *Manager) saveOriginalDNS(servers []string) {
	if len(servers) == 0 {
		return
	}
	dir, err := paths.Data()
	if err != nil {
		return
	}
	path := filepath.Join(dir, "dns_original.servers")
	if _, err := os.Stat(path); err == nil {
		return // já salvou antes
	}
	content := strings.Join(servers, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		m.buf.Warnf("dns", "falha ao salvar DNS originais: %v", err)
	}
	m.buf.Infof("dns", "DNS originais salvos em %s", path)
}

// loadOriginalDNS carrega os servidores DNS originais do arquivo de backup.
func (m *Manager) loadOriginalDNS() []string {
	dir, err := paths.Data()
	if err != nil {
		return nil
	}
	path := filepath.Join(dir, "dns_original.servers")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var servers []string
	for _, s := range strings.Fields(string(data)) {
		s = strings.TrimSpace(s)
		if s != "" {
			servers = append(servers, s)
		}
	}
	return servers
}

// extractNameservers extrai endereços de nameserver de um conteúdo de resolv.conf.
func extractNameservers(content string) []string {
	var servers []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "nameserver ") {
			ns := strings.TrimSpace(strings.TrimPrefix(line, "nameserver "))
			if ns != "" && ns != "127.0.0.1" {
				servers = append(servers, ns)
			}
		}
	}
	return servers
}

type multiWriter struct{ a, b interface{ Write([]byte) (int, error) } }

func (m multiWriter) Write(p []byte) (int, error) {
	m.a.Write(p)
	return m.b.Write(p)
}

func ioMulti(a, b interface{ Write([]byte) (int, error) }) interface{ Write([]byte) (int, error) } {
	return multiWriter{a, b}
}
