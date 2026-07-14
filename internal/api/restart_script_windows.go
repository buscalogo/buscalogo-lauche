//go:build windows

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

const createNewProcessGroup = 0x00000200

func scheduleDaemonRestart(daemon string, buf *logx.Buffer) error {
	home, err := paths.Home()
	if err != nil {
		return err
	}
	scriptPath := filepath.Join(home, "restart-agent.bat")
	script := fmt.Sprintf(`@echo off
REM Gerado pelo BuscaLogo Agent — reinício pós-atualização
set "DAEMON=%s"
timeout /t 2 /nobreak >nul
taskkill /F /IM buscalogo-agentd.exe >nul 2>&1
timeout /t 7 /nobreak >nul
set BUSCALOGO_POST_UPDATE=1
start "" "%%DAEMON%%" --no-tray
`, daemon)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return err
	}

	cmd := exec.Command("cmd", "/C", "start", "", scriptPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
	if err := cmd.Start(); err != nil {
		return err
	}
	buf.Infof("api", "reinício agendado (script=%s, pid=%d)", scriptPath, cmd.Process.Pid)
	return nil
}

func reexecDaemon(daemon string, args, env []string) error {
	argTail := args
	if len(argTail) > 0 {
		argTail = argTail[1:]
	}
	cmd := exec.Command(daemon, argTail...)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
