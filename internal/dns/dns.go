package dns

import (
	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/logx"
)

// SystemInfo descreve o ambiente DNS do host (campos Linux/Windows conforme plataforma).
type SystemInfo struct {
	Platform           string   `json:"platform"`
	HasSystemdResolved bool     `json:"has_systemd_resolved"`
	ResolvedStubActive bool     `json:"resolved_stub_active"`
	HasNetworkManager  bool     `json:"has_network_manager"`
	ResolvConfMode     string   `json:"resolv_conf_mode"` // stub | foreign | n/a
	Port53LoopbackFree bool     `json:"port_53_loopback_free"`
	Port53Listeners    []string `json:"port_53_listeners"`
	SetcapAvailable    bool     `json:"setcap_available"`
	AdminAvailable     bool     `json:"admin_available"`
	NrptConfigured     bool     `json:"nrpt_configured"`
	CurrentMode        string   `json:"current_mode"`
	UsesBuscaLogoDNS   bool     `json:"uses_buscalogo_dns"`
}

// Manager controla a troca entre Modo A (local :5333) e Modo B (system :53).
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

type multiWriter struct{ a, b interface{ Write([]byte) (int, error) } }

func (m multiWriter) Write(p []byte) (int, error) {
	_, _ = m.a.Write(p)
	return m.b.Write(p)
}

func ioMulti(a, b interface{ Write([]byte) (int, error) }) interface{ Write([]byte) (int, error) } {
	return multiWriter{a: a, b: b}
}
