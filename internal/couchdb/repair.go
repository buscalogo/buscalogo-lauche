package couchdb

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"buscalogo-agent/internal/paths"
	"buscalogo-agent/internal/process"
)

const watchdogInterval = 45 * time.Second

// StartWatchdog verifica periodicamente e repara o CouchDB se estiver parado.
func (s *Service) StartWatchdog() {
	if !s.cfg.CouchDB.Enabled {
		return
	}
	go func() {
		time.Sleep(20 * time.Second)
		for {
			if s.cfg.CouchDB.Enabled && s.needsRepair() {
				s.buf.Warnf("couchdb", "watchdog: CouchDB indisponível — reparando")
				if err := s.RepairAndStart(); err != nil {
					s.buf.Warnf("couchdb", "watchdog: reparo falhou: %v", err)
				}
			}
			time.Sleep(watchdogInterval)
		}
	}()
}

func (s *Service) needsRepair() bool {
	st := s.Status()
	if st.State == process.StateRunning {
		return !s.Reachable(nil)
	}
	return st.State == process.StateStopped || st.State == process.StateCrashed
}

// RepairAndStart limpa release quebrado, processos órfãos e sobe o CouchDB.
func (s *Service) RepairAndStart() error {
	if err := s.cleanupBrokenUserRelease(); err != nil {
		s.buf.Warnf("couchdb", "limpeza release: %v", err)
	}
	binary, _ := s.BinaryPath()
	_ = process.KillExistingByBinary(s.buf, "beam.smp", "")
	_ = process.KillExistingByBinary(s.buf, "epmd", "")
	if binary != "" {
		_ = process.KillExistingByBinary(s.buf, "couchdb", binary)
	} else {
		_ = process.KillExistingByBinary(s.buf, "couchdb", "")
	}

	if s.proc != nil {
		_ = s.proc.Stop()
		s.proc = nil
	}
	if err := s.Start(); err != nil {
		return err
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if s.Reachable(nil) {
			s.buf.Infof("couchdb", "reparo concluído — CouchDB respondendo")
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("CouchDB iniciou mas não respondeu em %s", s.BaseURL())
}

func (s *Service) cleanupBrokenUserRelease() error {
	bin, err := paths.Bin()
	if err != nil {
		return err
	}
	userRoot := filepath.Join(bin, releaseName)
	if !isExec(filepath.Join(userRoot, "bin", "couchdb")) {
		return nil
	}
	if releaseLooksComplete(userRoot) {
		return nil
	}
	s.buf.Warnf("couchdb", "removendo release incompleto em %s", userRoot)
	return os.RemoveAll(userRoot)
}
