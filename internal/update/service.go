package update

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"buscalogo-agent/internal/config"
	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/paths"
	"buscalogo-agent/internal/version"
)

const minCheckInterval = time.Hour

// Service verifica, baixa e instala atualizações do GitHub Releases.
type Service struct {
	cfg     *config.Config
	buf     *logx.Buffer
	product string

	mu          sync.RWMutex
	status      Status
	manifest    *Manifest
	lastCheck   time.Time
	lastForce   time.Time
	onInstalled func()
}

// New cria o atualizador do Agent (.deb).
func New(cfg *config.Config, buf *logx.Buffer) *Service {
	return NewProduct(cfg, buf, ProductAgent)
}

// NewProduct cria o atualizador para agent ou registry.
func NewProduct(cfg *config.Config, buf *logx.Buffer, product string) *Service {
	if product != ProductRegistry {
		product = ProductAgent
	}
	return &Service{
		cfg:     cfg,
		buf:     buf,
		product: product,
		status: Status{
			Current:    version.Version,
			State:      "idle",
			CanInstall: canInstallProduct(product),
			Product:    product,
		},
	}
}

func canInstallProduct(product string) bool {
	if product == ProductRegistry {
		return true
	}
	return paths.IsDebInstall()
}

func (s *Service) SetOnInstalled(fn func()) {
	s.onInstalled = fn
}

func (s *Service) ClearNeedsRestart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.NeedsRestart = false
}

func (s *Service) Product() string {
	return s.product
}

func (s *Service) StartBackground() {
	if !s.cfg.Update.EnabledOrDefault() {
		s.buf.Infof("update", "verificação de atualizações desabilitada")
		return
	}
	go func() {
		time.Sleep(15 * time.Second)
		s.backgroundCycle()
		interval := time.Duration(s.cfg.Update.CheckIntervalHoursOrDefault()) * time.Hour
		if interval < time.Hour {
			interval = 24 * time.Hour
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			s.backgroundCycle()
		}
	}()
}

func (s *Service) backgroundCycle() {
	st, err := s.Check(false)
	if err != nil {
		s.buf.Warnf("update", "check: %v", err)
		return
	}
	if s.product != ProductRegistry || !st.Available {
		return
	}
	s.buf.Infof("update", "registry: aplicando atualização automática %s → %s", st.Current, st.Latest)
	if _, err := s.Download(); err != nil {
		s.buf.Warnf("update", "download automático: %v", err)
		return
	}
	if _, err := s.Install(); err != nil {
		s.buf.Warnf("update", "install automático: %v", err)
	}
}

func (s *Service) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.status
	st.Current = version.Version
	st.CanInstall = canInstallProduct(s.product)
	st.Product = s.product
	return st
}

func (s *Service) assetFromManifest(m *Manifest) (debAsset, error) {
	if m == nil {
		return debAsset{}, fmt.Errorf("manifest nil")
	}
	if s.product == ProductRegistry {
		arch := runtime.GOARCH
		switch arch {
		case "arm64":
			a := m.LinuxARM64Registry
			if a.URL == "" {
				return debAsset{}, fmt.Errorf("manifest sem linux_arm64_registry (arch=%s)", arch)
			}
			return a, nil
		case "amd64":
			a := m.LinuxAMD64Registry
			if a.URL == "" {
				return debAsset{}, fmt.Errorf("manifest sem linux_amd64_registry (arch=%s)", arch)
			}
			return a, nil
		default:
			return debAsset{}, fmt.Errorf("arquitetura %s sem binário registry na release (suportado: amd64, arm64)", arch)
		}
	}
	a := m.LinuxAMD64Deb
	if a.URL == "" {
		return debAsset{}, fmt.Errorf("manifest sem linux_amd64_deb")
	}
	return a, nil
}

func (s *Service) Check(force bool) (Status, error) {
	if !s.cfg.Update.EnabledOrDefault() {
		return s.Status(), fmt.Errorf("atualizações desabilitadas na config")
	}
	s.mu.Lock()
	if !force && !s.lastCheck.IsZero() && time.Since(s.lastCheck) < minCheckInterval {
		st := s.status
		s.mu.Unlock()
		return st, nil
	}
	if force && !s.lastForce.IsZero() && time.Since(s.lastForce) < minCheckInterval {
		st := s.status
		s.mu.Unlock()
		return st, fmt.Errorf("aguarde %s antes de verificar novamente", (minCheckInterval - time.Since(s.lastForce)).Round(time.Second))
	}
	prevDebPath := s.status.DebPath
	prevState := s.status.State
	s.status.State = "checking"
	s.status.Error = ""
	s.mu.Unlock()

	rel, err := fetchLatestRelease(s.cfg.Update.GitHubRepoOrDefault())
	if err != nil {
		s.setError(err)
		return s.Status(), err
	}
	manifest, err := manifestFromRelease(rel)
	if err != nil {
		s.setError(err)
		return s.Status(), err
	}
	asset, err := s.assetFromManifest(manifest)
	if err != nil {
		s.setError(err)
		return s.Status(), err
	}

	latest := normalizeVersion(manifest.Version)
	current := version.Version
	available := version.Compare(latest, current) > 0

	s.mu.Lock()
	defer s.mu.Unlock()
	s.manifest = manifest
	s.lastCheck = time.Now()
	if force {
		s.lastForce = time.Now()
	}
	state := "idle"
	if prevDebPath != "" && prevState == "ready" {
		state = "ready"
	}
	s.status = Status{
		Current:    current,
		Latest:     latest,
		Available:  available,
		Notes:      firstNonEmpty(manifest.Notes, rel.Body),
		State:      state,
		DebURL:     asset.URL,
		LastCheck:  s.lastCheck.UnixMilli(),
		CanInstall: canInstallProduct(s.product),
		ReleaseURL: rel.HTMLURL,
		DebPath:    prevDebPath,
		Progress:   s.status.Progress,
		Product:    s.product,
	}
	if available {
		s.buf.Infof("update", "nova versão disponível: %s (atual %s) [%s]", latest, current, s.product)
	} else {
		s.buf.Infof("update", "sem atualizações (atual %s) [%s]", current, s.product)
	}
	return s.status, nil
}

func (s *Service) Download() (Status, error) {
	s.mu.RLock()
	m := s.manifest
	st := s.status
	s.mu.RUnlock()
	if m == nil || !st.Available {
		return s.Status(), fmt.Errorf("nenhuma atualização disponível — verifique primeiro")
	}
	asset, err := s.assetFromManifest(m)
	if err != nil {
		return s.Status(), err
	}

	dir, err := paths.UpdatesDir()
	if err != nil {
		return s.Status(), err
	}
	name := asset.Name
	if name == "" {
		if s.product == ProductRegistry {
			name = fmt.Sprintf("buscalogo-registry_%s_linux_%s", normalizeVersion(m.Version), runtime.GOARCH)
		} else {
			name = fmt.Sprintf("buscalogo-agent_%s_amd64.deb", normalizeVersion(m.Version))
		}
	}
	dest := filepath.Join(dir, name)

	s.mu.Lock()
	s.status.State = "downloading"
	s.status.Progress = 0
	s.status.Error = ""
	s.mu.Unlock()

	err = downloadFile(asset.URL, dest, func(done, total int64) {
		pct := 0
		if total > 0 {
			pct = int(done * 100 / total)
		}
		s.mu.Lock()
		s.status.Progress = pct
		s.mu.Unlock()
	})
	if err != nil {
		s.setError(err)
		return s.Status(), err
	}
	if asset.SHA256 != "" {
		if err := verifySHA256(dest, asset.SHA256); err != nil {
			_ = os.Remove(dest)
			s.setError(err)
			return s.Status(), err
		}
	}

	s.mu.Lock()
	s.status.State = "ready"
	s.status.Progress = 100
	s.status.DebPath = dest
	s.status.DebURL = asset.URL
	s.mu.Unlock()
	s.buf.Infof("update", "pacote baixado: %s", dest)
	return s.Status(), nil
}

func (s *Service) Install() (Status, error) {
	st := s.Status()
	if st.DebPath == "" {
		if st.Available && st.DebURL != "" {
			if _, err := s.Download(); err != nil {
				return s.Status(), err
			}
			st = s.Status()
		} else {
			return st, fmt.Errorf("nenhum pacote baixado")
		}
	}

	s.mu.RLock()
	m := s.manifest
	s.mu.RUnlock()
	if m != nil {
		if asset, err := s.assetFromManifest(m); err == nil && asset.SHA256 != "" {
			if err := verifySHA256(st.DebPath, asset.SHA256); err != nil {
				return s.Status(), err
			}
		}
	}

	s.mu.Lock()
	s.status.State = "installing"
	s.status.Error = ""
	s.mu.Unlock()

	var installErr error
	if s.product == ProductRegistry {
		installErr = InstallRegistryBinary(s.buf, st.DebPath)
	} else {
		installErr = InstallDeb(s.buf, st.DebPath)
	}
	if installErr != nil {
		s.setError(installErr)
		return s.Status(), installErr
	}

	s.mu.Lock()
	s.status.State = "done"
	s.status.NeedsRestart = true
	s.status.Progress = 100
	s.mu.Unlock()
	s.buf.Infof("update", "instalação concluída [%s]", s.product)

	if s.onInstalled != nil {
		go s.onInstalled()
	}
	return s.Status(), nil
}

func (s *Service) setError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.State = "error"
	s.status.Error = err.Error()
	s.buf.Warnf("update", "%v", err)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
