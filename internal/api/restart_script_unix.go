//go:build unix

package api

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/paths"
)

func scheduleDaemonRestart(daemon string, buf *logx.Buffer) error {
	if paths.IsAgentServerDebInstall() {
		return scheduleSystemdAgentRestart(buf)
	}
	home, err := paths.Home()
	if err != nil {
		return err
	}
	scriptPath := filepath.Join(home, "restart-agent.sh")
	script := fmt.Sprintf(`#!/bin/sh
# Gerado pelo BuscaLogo Agent — reinício completo (órfãos + daemon)
set -e
DAEMON=%q
sleep 2
pkill -TERM -f 'buscalogo-agentd' 2>/dev/null || true
sleep 2
# Serviços embutidos que às vezes ficam órfãos ao fechar o Launch
pkill -TERM -f 'yggdrasil.*buscalogo|buscalogo.*yggdrasil' 2>/dev/null || true
pkill -TERM -f 'coredns.*buscalogo|buscalogo.*coredns|coredns.*Corefile' 2>/dev/null || true
pkill -f beam.smp 2>/dev/null || true
pkill -f epmd 2>/dev/null || true
sleep 4
pkill -KILL -f 'buscalogo-agentd' 2>/dev/null || true
export BUSCALOGO_POST_UPDATE=1
exec "$DAEMON" --no-tray
`, daemon)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return err
	}

	cmd := exec.Command("sh", scriptPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	buf.Infof("api", "reinício agendado (script=%s, pid=%d)", scriptPath, cmd.Process.Pid)
	return nil
}

func scheduleSystemdAgentRestart(buf *logx.Buffer) error {
	systemctl, err := exec.LookPath("systemctl")
	if err != nil {
		return fmt.Errorf("systemctl: %w", err)
	}
	script := `#!/bin/sh
sleep 2
systemctl restart buscalogo-agent.service
`
	home, err := paths.Home()
	if err != nil {
		return err
	}
	scriptPath := filepath.Join(home, "restart-agent.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("sh", scriptPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		// Fallback direto
		cmd = exec.Command(systemctl, "restart", "buscalogo-agent.service")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err2 := cmd.Start(); err2 != nil {
			return err
		}
	}
	buf.Infof("api", "reinício systemd agendado (buscalogo-agent.service)")
	return nil
}

func reexecDaemon(daemon string, args, env []string) error {
	return syscall.Exec(daemon, args, env)
}
