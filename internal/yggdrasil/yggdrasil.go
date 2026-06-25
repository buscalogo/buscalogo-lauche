package yggdrasil

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"buscalogo-agent/assets"
	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/paths"
	"buscalogo-agent/internal/process"
)

const binaryName = "yggdrasil"

type Service struct {
	cfg  *config.Config
	buf  *logx.Buffer
	proc *process.Managed
}

func New(cfg *config.Config, buf *logx.Buffer) *Service {
	return &Service{cfg: cfg, buf: buf}
}

func (s *Service) BinaryPath() (string, error) {
	isExec := func(p string) bool {
		info, err := os.Stat(p)
		return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
	}
	switch s.cfg.Yggdrasil.Mode {
	case "external":
		for _, candidate := range []string{s.cfg.Yggdrasil.ExternalBinary, "/usr/bin/yggdrasil", "/usr/local/bin/yggdrasil"} {
			if candidate != "" && isExec(candidate) {
				return candidate, nil
			}
		}
		return "", fmt.Errorf("modo external mas nenhum binário yggdrasil encontrado")
	default:
		// Em instalações .deb os binários ficam em /opt/buscalogo/data/bin/ e já têm capabilities.
		for _, candidate := range []string{"/opt/buscalogo/data/bin/yggdrasil", "/usr/local/bin/yggdrasil", "/usr/bin/yggdrasil"} {
			if isExec(candidate) {
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
		return "", fmt.Errorf("binário %s não encontrado (embuta com 'make assets')", binaryName)
	}
}

func (s *Service) ConfPath() (string, error) {
	data, err := paths.Data()
	if err != nil {
		return "", err
	}
	return filepath.Join(data, "yggdrasil.conf"), nil
}

// backupConfPath retorna o caminho do backup da config dentro do data dir.
func backupConfPath() (string, error) {
	data, err := paths.Data()
	if err != nil {
		return "", err
	}
	return filepath.Join(data, "yggdrasil.conf.bak"), nil
}

// emergencyKeyPath retorna o caminho do backup de emergência da chave privada.
// Fica em ~/.config/buscalogo/ — fora do data dir, sobrevive a rm -rf ~/.buscalogo.
func emergencyKeyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "buscalogo", "yggdrasil.key"), nil
}

func adminSocketPath() (string, error) {
	data, err := paths.Data()
	if err != nil {
		return "", err
	}
	return filepath.Join(data, "yggdrasil.sock"), nil
}

type confDoc struct {
	PrivateKey          string              `json:"PrivateKey"`
	Peers               []string            `json:"Peers"`
	InterfacePeers      map[string][]string `json:"InterfacePeers"`
	Listen              []string            `json:"Listen"`
	MulticastInterfaces []any               `json:"MulticastInterfaces"`
	AllowedPublicKeys   []string            `json:"AllowedPublicKeys"`
	GroupPassword       string              `json:"GroupPassword"`
	IfName              string              `json:"IfName"`
	IfMTU               int                 `json:"IfMTU"`
	NodeInfoPrivacy     bool                `json:"NodeInfoPrivacy"`
	NodeInfo            map[string]string   `json:"NodeInfo,omitempty"`
	AdminListen         string              `json:"AdminListen"`
}

// ensureListen garante que Listen tenha pelo menos um endereço.
// Sem um listener, Yggdrasil 0.5.x pode não estabelecer conexões de saída.
func ensureListen(doc *confDoc) {
	if len(doc.Listen) == 0 {
		doc.Listen = []string{"tcp://0.0.0.0:0"}
	}
}

func (s *Service) ensureConf(binary string) (string, error) {
	path, err := s.ConfPath()
	if err != nil {
		return "", err
	}

	// 1. Config existe — usar e atualizar backup.
	if _, err := os.Stat(path); err == nil {
		if err := s.injectPeers(path); err != nil {
			s.buf.Warnf("yggdrasil", "falha ao injetar peers em config existente: %v", err)
		}
		s.logConfigStats(path)
		s.saveBackups(path)
		return path, nil
	}

	s.buf.Warnf("yggdrasil", "config não encontrada em %s", path)

	// 2. Tentar restaurar do backup local (yggdrasil.conf.bak).
	if bakPath, err := backupConfPath(); err == nil {
		if data, err := os.ReadFile(bakPath); err == nil && len(data) > 0 {
			s.buf.Warnf("yggdrasil", "restaurando identidade do backup local %s", bakPath)
			if err := s.restoreFromData(path, data); err == nil {
				s.saveBackups(path)
				return path, nil
			}
			s.buf.Errorf("yggdrasil", "falha ao restaurar backup local: %v", err)
		}
	}

	// 3. Tentar restaurar da chave de emergência (~/.config/buscalogo/yggdrasil.key).
	if keyPath, err := emergencyKeyPath(); err == nil {
		if keyData, err := os.ReadFile(keyPath); err == nil {
			privKey := strings.TrimSpace(string(keyData))
			if privKey != "" {
				s.buf.Warnf("yggdrasil", "restaurando identidade da chave de emergência %s", keyPath)
				if conf, err := s.generateConfFromKey(binary, privKey); err == nil {
					if err := os.WriteFile(path, conf, 0o600); err == nil {
						s.saveBackups(path)
						return path, nil
					}
				}
				s.buf.Errorf("yggdrasil", "falha ao restaurar de chave de emergência")
			}
		}
	}

	// 4. Nenhum backup — gerar nova identidade (AVISO).
	s.buf.Errorf("yggdrasil", "!!! NENHUMA IDENTIDADE ENCONTRADA — gerando nova chave !!!")
	s.buf.Errorf("yggdrasil", "!!! O endereço IPv6 do nó VAI MUDAR — sites .bl ficarão inacessíveis pelo endereço antigo !!!")
	return s.generateNewIdentity(binary, path)
}

// generateNewIdentity executa yggdrasil -genconf e cria a config inicial.
func (s *Service) generateNewIdentity(binary, path string) (string, error) {
	s.buf.Infof("yggdrasil", "gerando config inicial (nova identidade de nó)")
	out, err := exec.Command(binary, "-json", "-genconf").Output()
	if err != nil {
		return "", fmt.Errorf("genconf: %w", err)
	}
	var doc confDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		return "", fmt.Errorf("parse genconf: %w", err)
	}
	doc.Peers = normalizePeers(s.cfg.Yggdrasil.Peers)
	ensureMulticast(&doc)
	ensureListen(&doc)
	doc.NodeInfo = map[string]string{"name": s.cfg.Node.Name}
	if sock, err := adminSocketPath(); err == nil {
		doc.AdminListen = "unix://" + sock
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	s.saveBackups(path)
	s.buf.Infof("yggdrasil", "nova identidade criada em %s (%d peers, %d multicast ifs)", path, len(doc.Peers), len(doc.MulticastInterfaces))
	return path, nil
}

// restoreFromData escreve dados de backup no caminho da config e injeta peers.
func (s *Service) restoreFromData(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	if err := s.injectPeers(path); err != nil {
		s.buf.Warnf("yggdrasil", "falha ao injetar peers na config restaurada: %v", err)
	}
	return nil
}

// generateConfFromKey gera uma config completa a partir de uma chave privada existente.
func (s *Service) generateConfFromKey(binary, privKey string) ([]byte, error) {
	out, err := exec.Command(binary, "-json", "-genconf").Output()
	if err != nil {
		return nil, fmt.Errorf("genconf: %w", err)
	}
	var doc confDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("parse genconf: %w", err)
	}
	doc.PrivateKey = privKey
	doc.Peers = normalizePeers(s.cfg.Yggdrasil.Peers)
	ensureMulticast(&doc)
	ensureListen(&doc)
	doc.NodeInfo = map[string]string{"name": s.cfg.Node.Name}
	if sock, err := adminSocketPath(); err == nil {
		doc.AdminListen = "unix://" + sock
	}
	return json.MarshalIndent(doc, "", "  ")
}

// saveBackups copia a config atual para:
// 1. yggdrasil.conf.bak (backup local, mesmo data dir)
// 2. ~/.config/buscalogo/yggdrasil.key (só a chave privada, fora do data dir)
func (s *Service) saveBackups(confPath string) {
	data, err := os.ReadFile(confPath)
	if err != nil {
		return
	}

	// Backup local
	if bakPath, err := backupConfPath(); err == nil {
		if err := os.WriteFile(bakPath, data, 0o600); err != nil {
			s.buf.Warnf("yggdrasil", "falha ao salvar backup local: %v", err)
		}
	}

	// Backup de emergência (só a chave privada)
	var doc confDoc
	if json.Unmarshal(data, &doc) == nil && doc.PrivateKey != "" {
		if keyPath, err := emergencyKeyPath(); err == nil {
			if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
				s.buf.Warnf("yggdrasil", "falha ao criar dir de emergência: %v", err)
				return
			}
			if err := os.WriteFile(keyPath, []byte(doc.PrivateKey), 0o600); err != nil {
				s.buf.Warnf("yggdrasil", "falha ao salvar chave de emergência: %v", err)
			}
		}
	}
}

// ExportIdentity retorna a chave privada e endereço para migração.
func (s *Service) ExportIdentity() (privateKey string, err error) {
	path, err := s.ConfPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var doc confDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", err
	}
	return doc.PrivateKey, nil
}

// ImportIdentity substitui a chave privada, reescreve a config e reinicia.
func (s *Service) ImportIdentity(privateKey string) error {
	privateKey = strings.TrimSpace(privateKey)
	if privateKey == "" {
		return fmt.Errorf("chave privada vazia")
	}
	binary, err := s.BinaryPath()
	if err != nil {
		return err
	}
	conf, err := s.generateConfFromKey(binary, privateKey)
	if err != nil {
		return fmt.Errorf("gerar config da chave: %w", err)
	}
	path, err := s.ConfPath()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, conf, 0o600); err != nil {
		return err
	}
	s.saveBackups(path)
	s.buf.Infof("yggdrasil", "identidade importada — reiniciando com nova chave")
	return s.Restart()
}

func (s *Service) injectPeers(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc confDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	doc.Peers = normalizePeers(s.cfg.Yggdrasil.Peers)
	ensureMulticast(&doc)
	ensureListen(&doc)
	if sock, err := adminSocketPath(); err == nil {
		want := "unix://" + sock
		if doc.AdminListen != want {
			doc.AdminListen = want
			s.buf.Infof("yggdrasil", "AdminListen ajustado para %s", want)
		}
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

func (s *Service) logConfigStats(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var doc confDoc
	if json.Unmarshal(data, &doc) != nil {
		return
	}
	s.buf.Infof("yggdrasil", "config: %d peers, %d multicast interfaces, %d allowed keys",
		len(doc.Peers), len(doc.MulticastInterfaces), len(doc.AllowedPublicKeys))
}

// cleanupAdminSocket remove o socket unix se ele existir.
// Diferentemente de remover o arquivo de um socket TCP comum, remover o arquivo de um
// socket Unix é seguro: o inode do socket permanece enquanto houver um fd aberto para ele,
// e Yggdrasil consegue criar um socket novo no mesmo caminho — o bind não conflita porque
// cria um inode diferente.
func cleanupAdminSocket(buf *logx.Buffer) error {
	sock, err := adminSocketPath()
	if err != nil {
		return err
	}
	fi, err := os.Stat(sock)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.Mode()&os.ModeSocket == 0 {
		return nil
	}
	return os.Remove(sock)
}

func normalizePeers(in []string) []string {
	var out []string
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// ensureMulticast verifica se MulticastInterfaces está configurado no doc.
// Se estiver vazio, define o padrão que permite descoberta automática na LAN.
func ensureMulticast(doc *confDoc) {
	if len(doc.MulticastInterfaces) == 0 {
		doc.MulticastInterfaces = []any{
			map[string]any{
				"Regex":   ".*",
				"Beacon":  true,
				"Listen":  true,
				"Port":    0,
				"Priority": 0,
			},
		}
	}
}

func (s *Service) Start() error {
	binary, err := s.BinaryPath()
	if err != nil {
		return err
	}
	conf, err := s.ensureConf(binary)
	if err != nil {
		return err
	}
	// Mata processos antigos PRIMEIRO, depois limpa o socket stale.
	// A ordem importa: se limpamos o socket antes de matar o processo,
	// o socket ainda está ativo e cleanupAdminSocket não remove.
	if err := process.KillExistingByBinary(s.buf, "yggdrasil", binary); err != nil {
		s.buf.Warnf("yggdrasil", "limpeza de processos antigos: %v", err)
	}
	if err := cleanupAdminSocket(s.buf); err != nil {
		s.buf.Warnf("yggdrasil", "falha ao limpar socket admin: %v", err)
	}
	if s.proc == nil {
		s.proc = process.New(process.Options{
			Name:        "Yggdrasil",
			Binary:      binary,
			Args:        []string{"-useconffile", conf},
			LogSource:   "yggdrasil",
			LogBuf:      s.buf,
			AutoRestart: true,
			PreStart: func() error {
				// Mata qualquer Yggdrasil órfão de outra instância do agente
				// ANTES de limpar o socket. Essencial no auto-restart: sem isso,
				// o socket fica "in use" e o novo processo morre.
				binary, _ := s.BinaryPath()
				if err := process.KillExistingByBinary(s.buf, "yggdrasil", binary); err != nil {
					s.buf.Warnf("yggdrasil", "PreStart: limpeza de processos: %v", err)
				}
				// Agora sim, remove o socket stale.
				return cleanupAdminSocket(s.buf)
			},
		})
	}
	return s.proc.Start()
}

func (s *Service) Stop() error {
	if s.proc == nil {
		return nil
	}
	return s.proc.Stop()
}

func (s *Service) Restart() error {
	if s.proc == nil {
		return s.Start()
	}
	return s.proc.Restart()
}

func (s *Service) Status() process.Status {
	if s.proc == nil {
		return process.Status{Name: "Yggdrasil", State: process.StateStopped}
	}
	return s.proc.Status()
}

func (s *Service) Managed() *process.Managed { return s.proc }

// SelfAddress retorna o endereço IPv6 Yggdrasil deste nó, ou "" se indisponível.
func (s *Service) SelfAddress() string {
	info := s.AdminInfo()
	if info == nil || info.Self == nil {
		return ""
	}
	return info.Self.Address
}
