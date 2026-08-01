//go:build unix

package couchdb

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"buscalogo-agent/internal/process"
)

// killErlangOrphans mata beam.smp/epmd do CouchDB BuscaLogo deixados por Agents anteriores.
// KillExistingByBinary(couchdb, wrapper) não acha o runtime real (beam.smp).
func (s *Service) killErlangOrphans() {
	root, _ := s.ReleaseRoot()
	root = filepath.Clean(root)
	markers := []string{
		"-progname couchdb",
		"couchdb@127.0.0.1",
		"/buscalogo/",
		"/.buscalogo/",
	}
	if root != "" && root != "." {
		markers = append(markers, root)
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	self := os.Getpid()
	killed := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		cmd := strings.ReplaceAll(string(cmdline), "\x00", " ")
		isBeam := strings.Contains(cmd, "/beam.smp") || strings.HasPrefix(cmd, "beam.smp ")
		isEpmd := strings.Contains(cmd, "/epmd") && strings.Contains(cmd, "erts-")
		if !isBeam && !isEpmd {
			continue
		}
		if isBeam && !strings.Contains(cmd, "couchdb") {
			continue
		}
		match := false
		for _, m := range markers {
			if m != "" && strings.Contains(cmd, m) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		// Filho direto deste agentd = Managed atual — não matar.
		if ppidOfProc(pid) == self {
			continue
		}
		if s.buf != nil {
			name := "beam.smp"
			if isEpmd {
				name = "epmd"
			}
			s.buf.Warnf("couchdb", "matando %s órfão %d", name, pid)
		}
		if err := process.KillProcess(pid); err == nil {
			killed++
		}
	}
	if killed > 0 {
		time.Sleep(800 * time.Millisecond)
	}
}

func ppidOfProc(pid int) int {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return -1
	}
	s := string(data)
	i := strings.LastIndex(s, ")")
	if i < 0 || i+2 >= len(s) {
		return -1
	}
	fields := strings.Fields(s[i+2:])
	if len(fields) < 2 {
		return -1
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return -1
	}
	return ppid
}
