package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/paths"
	"buscalogo-agent/internal/system"
)

func installScriptPath() string {
	candidates := []string{
		"/opt/buscalogo/update-install.sh",
	}
	if exe, err := os.Executable(); err == nil {
		exe, _ = filepath.EvalSymlinks(exe)
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "update-install.sh"))
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// PreferAgentServerDeb informa se o update deve baixar o .deb server (systemd).
func PreferAgentServerDeb() bool {
	return paths.IsAgentServerDebInstall()
}

const agentSystemdUnit = "buscalogo-agent.service"

// InstallDeb instala o .deb com privilégios (pkexec).
func InstallDeb(buf *logx.Buffer, debPath string) error {
	if !paths.IsDebInstall() {
		return fmt.Errorf("atualização automática só está disponível na instalação .deb em /opt/buscalogo")
	}
	script := installScriptPath()
	if script == "" {
		return fmt.Errorf("script update-install.sh não encontrado")
	}
	if _, err := os.Stat(debPath); err != nil {
		return fmt.Errorf("pacote não encontrado: %w", err)
	}
	buf.Infof("update", "instalando %s via %s", debPath, script)
	out, err := system.RunPrivileged(buf, script, debPath)
	if err != nil {
		return fmt.Errorf("instalação: %w (%s)", err, string(out))
	}
	// Desktop update-install.sh só faz dpkg; server script já reinicia systemd.
	// Se for server e o script for o genérico antigo, reforça restart.
	if PreferAgentServerDeb() {
		_ = tryRestartAgentSystemd(buf)
	}
	return nil
}

func tryRestartAgentSystemd(buf *logx.Buffer) bool {
	systemctl, err := exec.LookPath("systemctl")
	if err != nil {
		return false
	}
	managed := exec.Command(systemctl, "is-active", "--quiet", agentSystemdUnit).Run() == nil ||
		exec.Command(systemctl, "is-enabled", "--quiet", agentSystemdUnit).Run() == nil
	if !managed {
		return false
	}
	cmd := exec.Command(systemctl, "restart", agentSystemdUnit)
	if err := cmd.Start(); err != nil {
		if buf != nil {
			buf.Warnf("update", "systemctl restart %s: %v", agentSystemdUnit, err)
		}
		return false
	}
	go func() {
		time.Sleep(2 * time.Second)
		_ = cmd.Wait()
	}()
	if buf != nil {
		buf.Infof("update", "systemctl restart %s disparado", agentSystemdUnit)
	}
	return true
}
