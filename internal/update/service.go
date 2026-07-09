package update

import (
	"fmt"
	"os"
	"path/filepath"
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
	cfg *config.Config
	buf *logx.Buffer

	mu          sync.RWMutex
	status      Status
	manifest    *Manifest
	lastCheck   time.Time
	lastForce   time.Time
	onInstalled func()
}

func New(cfg *config.Config, buf *logx.Buffer) *Service {
	return &Service{
		cfg: cfg,
		buf: buf,
		status: Status{
			Current:    version.Version,
			State:      "idle",
			CanInstall: paths.IsDebInstall(),
		},
	}
}

func (s *Service) SetOnInstalled(fn func()) {
	s.onInstalled = fn
}

func (s *Service) ClearNeedsRestart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.NeedsRestart = false
}

func (s *Service) StartBackground() {
	if !s.cfg.Update.EnabledOrDefault() {
		s.buf.Infof("update", "verificação de atualizações desabilitada")
		return
	}
	go func() {
		time.Sleep(15 * time.Second)
		if _, err := s.Check(false); err != nil {
			s.buf.Warnf("update", "check inicial: %v", err)
		}
		interval := time.Duration(s.cfg.Update.CheckIntervalHoursOrDefault()) * time.Hour
		if interval < time.Hour {
			interval = 24 * time.Hour
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := s.Check(false); err != nil {
				s.buf.Warnf("update", "check periódico: %v", err)
			}
		}
	}()
}

func (s *Service) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.status
	st.Current = version.Version
	st.CanInstall = paths.IsDebInstall()
	return st
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
		DebURL:     manifest.LinuxAMD64Deb.URL,
		LastCheck:  s.lastCheck.UnixMilli(),
		CanInstall: paths.IsDebInstall(),
		ReleaseURL: rel.HTMLURL,
		DebPath:    prevDebPath,
		Progress:   s.status.Progress,
	}
	if available {
		s.buf.Infof("update", "nova versão disponível: %s (atual %s)", latest, current)
	} else {
		s.buf.Infof("update", "sem atualizações (atual %s)", current)
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
	if m.LinuxAMD64Deb.URL == "" {
		return s.Status(), fmt.Errorf("manifest sem URL do .deb")
	}

	dir, err := paths.UpdatesDir()
	if err != nil {
		return s.Status(), err
	}
	name := m.LinuxAMD64Deb.Name
	if name == "" {
		name = fmt.Sprintf("buscalogo-agent_%s_amd64.deb", normalizeVersion(m.Version))
	}
	dest := filepath.Join(dir, name)

	s.mu.Lock()
	s.status.State = "downloading"
	s.status.Progress = 0
	s.status.Error = ""
	s.mu.Unlock()

	err = downloadFile(m.LinuxAMD64Deb.URL, dest, func(done, total int64) {
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
	if m.LinuxAMD64Deb.SHA256 != "" {
		if err := verifyDeb(dest, m.LinuxAMD64Deb.SHA256); err != nil {
			_ = os.Remove(dest)
			s.setError(err)
			return s.Status(), err
		}
	}

	s.mu.Lock()
	s.status.State = "ready"
	s.status.Progress = 100
	s.status.DebPath = dest
	s.status.DebURL = m.LinuxAMD64Deb.URL
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
	if m != nil && m.LinuxAMD64Deb.SHA256 != "" {
		if err := verifyDeb(st.DebPath, m.LinuxAMD64Deb.SHA256); err != nil {
			return s.Status(), err
		}
	}

	s.mu.Lock()
	s.status.State = "installing"
	s.status.Error = ""
	s.mu.Unlock()

	if err := InstallDeb(s.buf, st.DebPath); err != nil {
		s.setError(err)
		return s.Status(), err
	}

	s.mu.Lock()
	s.status.State = "done"
	s.status.NeedsRestart = true
	s.status.Progress = 100
	s.mu.Unlock()
	s.buf.Infof("update", "instalação concluída — reiniciando agente")

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
