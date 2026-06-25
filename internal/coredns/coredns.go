package coredns

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"buscalogo-agent/assets"
	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/paths"
	"buscalogo-agent/internal/process"
)

const binaryName = "coredns"

type Service struct {
	cfg  *config.Config
	buf  *logx.Buffer
	proc *process.Managed
}

func New(cfg *config.Config, buf *logx.Buffer) *Service {
	return &Service{cfg: cfg, buf: buf}
}

func (s *Service) BinaryPath() (string, error) {
	// Em instalações .deb os binários ficam em /opt/buscalogo/data/bin/ e já têm capabilities.
	for _, candidate := range []string{
		"/opt/buscalogo/data/bin/coredns",
		"/usr/local/bin/coredns",
		"/usr/bin/coredns",
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	bin, err := paths.Bin()
	if err != nil {
		return "", err
	}
	if assets.Has(binaryName) {
		return assets.Ensure(binaryName, bin)
	}
	return "", fmt.Errorf("binário %s não encontrado (embuta com 'make assets' ou instale no sistema)", binaryName)
}

func (s *Service) CorefilePath() (string, error) {
	data, err := paths.Data()
	if err != nil {
		return "", err
	}
	return filepath.Join(data, "Corefile"), nil
}

func (s *Service) WriteCorefile() (string, error) {
	path, err := s.CorefilePath()
	if err != nil {
		return "", err
	}
	data, err := paths.Data()
	if err != nil {
		return "", err
	}
	cacheDir := filepath.Join(data, "dns-cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}

	addr := fmt.Sprintf("%s:%d", s.cfg.DNS.Listen, s.cfg.DNS.Port)
	if s.cfg.DNS.Mode == "system" {
		addr = "127.0.0.1:53"
	}

	upstreams := s.cfg.DNS.Upstream
	if len(upstreams) == 0 {
		upstreams = []string{"1.1.1.1", "8.8.8.8"}
	}
	for i, u := range upstreams {
		if !strings.Contains(u, ":") {
			upstreams[i] = u + ":53"
		}
	}

	corefile := renderCorefile(addr, cacheDir, upstreams, s.cfg.Yggdrasil.DnsServers, s.cfg.Yggdrasil.Enabled, s.cfg.DNS.SearchDomains, s.hostsFile(), s.cfg.DNS.ExternalListen)
	if err := os.WriteFile(path, []byte(corefile), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Service) hostsFile() string {
	if f, err := paths.SitesHostsFile(); err == nil {
		return f
	}
	return "/etc/hosts"
}

func renderCorefile(addr, cacheDir string, upstreams, yggdns []string, yggEnabled bool, blTLDs []string, hostsFile string, externalListen bool) string {
	host := addr
	port := "53"
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
		port = addr[i+1:]
	}

	bind := host
	if externalListen {
		bind = "::"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Corefile gerado pelo BuscaLogo Agent — NAO EDITAR MANUALMENTE\n")
	fmt.Fprintf(&b, "# Endereco: %s\n", addr)
	fmt.Fprintf(&b, ".:%s {\n", port)
	fmt.Fprintf(&b, "    bind %s\n", bind)
	fmt.Fprintf(&b, "    errors\n")
	fmt.Fprintf(&b, "    health :5335 {\n")
	fmt.Fprintf(&b, "        lameduck 5s\n")
	fmt.Fprintf(&b, "    }\n")
	fmt.Fprintf(&b, "    ready :5336\n")
	fmt.Fprintf(&b, "    # Hosts de sites .bl hospedados neste agente\n")
	fmt.Fprintf(&b, "    hosts %s {\n", hostsFile)
	fmt.Fprintf(&b, "        fallthrough\n")
	fmt.Fprintf(&b, "    }\n")
	fmt.Fprintf(&b, "    cache {\n")
	fmt.Fprintf(&b, "        success %d 5\n", 4096)
	fmt.Fprintf(&b, "        denial 1024 5\n")
	fmt.Fprintf(&b, "    }\n")
	// Forward para .ygg via resolvedores Yggdrasil
	if yggEnabled && len(yggdns) > 0 {
		fmt.Fprintf(&b, "    # Yggdrasil DNS — Alfis, Meshname, etc\n")
		fmt.Fprintf(&b, "    forward ygg %s {\n", strings.Join(yggdns, " "))
		fmt.Fprintf(&b, "        max_concurrent 1000\n")
		fmt.Fprintf(&b, "        expire 10s\n")
		fmt.Fprintf(&b, "    }\n")
	}
	fmt.Fprintf(&b, "    # TLDs BuscaLogo: %s\n", strings.Join(blTLDs, ", "))
	fmt.Fprintf(&b, "    forward . %s {\n", strings.Join(upstreams, " "))
	fmt.Fprintf(&b, "        max_concurrent 1000\n")
	fmt.Fprintf(&b, "        expire 10s\n")
	fmt.Fprintf(&b, "    }\n")
	fmt.Fprintf(&b, "    reload 2s\n")
	fmt.Fprintf(&b, "}\n")
	return b.String()
}

func (s *Service) Start() error {
	binary, err := s.BinaryPath()
	if err != nil {
		return err
	}
	if err := s.cleanupOldProcesses(); err != nil {
		s.buf.Warnf("coredns", "limpeza de processos antigos: %v", err)
	}
	corefile, err := s.WriteCorefile()
	if err != nil {
		return err
	}
	if s.proc == nil {
		s.proc = process.New(process.Options{
			Name:        "CoreDNS",
			Binary:      binary,
			Args:        []string{"-conf", corefile},
			LogSource:   "coredns",
			LogBuf:      s.buf,
			AutoRestart: true,
		})
	}
	return s.proc.Start()
}

func (s *Service) cleanupOldProcesses() error {
	binary, err := s.BinaryPath()
	if err != nil {
		return err
	}
	return process.KillExistingByBinary(s.buf, "coredns", binary)
}

func (s *Service) Stop() error {
	if s.proc == nil {
		return nil
	}
	return s.proc.Stop()
}

func (s *Service) Restart() error {
	if _, err := s.WriteCorefile(); err != nil {
		return err
	}
	if s.proc == nil {
		return s.Start()
	}
	return s.proc.Restart()
}

func (s *Service) Status() process.Status {
	if s.proc == nil {
		return process.Status{Name: "CoreDNS", State: process.StateStopped}
	}
	return s.proc.Status()
}

func (s *Service) Managed() *process.Managed { return s.proc }
