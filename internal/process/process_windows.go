//go:build windows

package process

import (
	"os"
	"os/exec"
	"syscall"

	"buscalogo-agent/internal/logx"
)

func killProcessGroup(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

func forceKillProcessGroup(pid int) {
	killProcessGroup(pid)
}

func signalProcess(p *os.Process) {
	_ = p.Kill()
}

func setSpawnSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// KillExistingByBinary no Windows não varre /proc; best-effort noop.
// Órfãos devem ser tratados via Process.Kill do Managed.
func KillExistingByBinary(_ *logx.Buffer, _, _ string) error {
	return nil
}
