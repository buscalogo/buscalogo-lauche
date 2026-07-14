//go:build unix

package process

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"buscalogo-agent/internal/logx"
)

func killProcessGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
}

func forceKillProcessGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

func signalProcess(p *os.Process) {
	_ = p.Signal(syscall.SIGTERM)
}

func setSpawnSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		// Sem Pdeathsig: no Go a signal de morte do pai é ligada à OS thread do
		// Start(), não ao processo — a thread pode sumir e o filho morrer sozinho.
	}
}

// KillExistingByBinary mata processos órfãos cujo cmdline contém o binário.
// Não mata filhos diretos deste agente (PPID == getpid) — evita suicídio do
// processo gerenciado ao chamar Start/PreStart com o serviço já rodando.
func KillExistingByBinary(buf *logx.Buffer, name, binary string) error {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return err
	}
	binaryName := filepath.Base(binary)
	self := os.Getpid()
	killed := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if pid == self {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		cmd := strings.ReplaceAll(string(cmdline), "\x00", " ")
		if !cmdlineMatchesBinary(cmd, binary, binaryName) {
			continue
		}
		// Avoid killing system-wide instances.
		if strings.Contains(cmd, "/usr/bin/"+binaryName) || strings.Contains(cmd, "/usr/local/bin/"+binaryName) {
			continue
		}
		// Filho direto do agente = processo sob Managed; não matar.
		if ppidOf(pid) == self {
			continue
		}
		if buf != nil {
			buf.Warnf(name, "matando processo antigo %d: %s", pid, cmd)
		}
		if err := KillProcess(pid); err != nil {
			if buf != nil {
				buf.Warnf(name, "falha ao matar %d: %v", pid, err)
			}
		} else {
			killed++
		}
	}
	if killed > 0 {
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

func ppidOf(pid int) int {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return -1
	}
	// /proc/pid/stat: pid (comm) state ppid ...
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
