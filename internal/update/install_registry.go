package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"buscalogo-agent/internal/logx"
	"buscalogo-agent/internal/paths"
	"buscalogo-agent/internal/system"
)

const (
	pm2AppName  = "buscalogo-registry"
	systemdUnit = "buscalogo-registry.service"
)

// InstallRegistryArtifact instala .deb (dpkg) ou binário solto, conforme o artefato.
func InstallRegistryArtifact(buf *logx.Buffer, downloaded string) error {
	if strings.HasSuffix(strings.ToLower(downloaded), ".deb") {
		return InstallRegistryDeb(buf, downloaded)
	}
	return InstallRegistryBinary(buf, downloaded)
}

// InstallRegistryDeb instala o .deb do registry (reinicia via systemd no postinst/script).
func InstallRegistryDeb(buf *logx.Buffer, debPath string) error {
	if _, err := os.Stat(debPath); err != nil {
		return fmt.Errorf("pacote não encontrado: %w", err)
	}
	script := registryUpdateInstallScript()
	if script != "" {
		if buf != nil {
			buf.Infof("update", "instalando registry .deb via %s", script)
		}
		out, err := system.RunPrivileged(buf, script, debPath)
		if err != nil {
			return fmt.Errorf("instalação .deb: %w (%s)", err, string(out))
		}
		return nil
	}
	// Fallback: dpkg direto (root / sudo).
	if buf != nil {
		buf.Infof("update", "instalando registry .deb via dpkg -i")
	}
	out, err := system.RunPrivileged(buf, "dpkg", "-i", debPath)
	if err != nil {
		return fmt.Errorf("dpkg -i: %w (%s)", err, string(out))
	}
	_ = exec.Command("systemctl", "restart", systemdUnit).Start()
	return nil
}

func registryUpdateInstallScript() string {
	candidates := []string{
		"/opt/buscalogo-registry/update-install.sh",
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

// InstallRegistryBinary troca o executável atual pelo binário baixado e reinicia.
// Ordem: systemd → PM2 → re-exec do próprio processo.
func InstallRegistryBinary(buf *logx.Buffer, downloaded string) error {
	if _, err := os.Stat(downloaded); err != nil {
		return fmt.Errorf("binário baixado não encontrado: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("evalsymlinks: %w", err)
	}
	dir := filepath.Dir(exe)
	tmp := filepath.Join(dir, filepath.Base(exe)+".new")
	bak := filepath.Join(dir, filepath.Base(exe)+".old")

	in, err := os.ReadFile(downloaded)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, in, 0o755); err != nil {
		return fmt.Errorf("escrever %s: %w", tmp, err)
	}
	_ = os.Remove(bak)
	if err := os.Rename(exe, bak); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("backup %s: %w", exe, err)
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Rename(bak, exe) // rollback
		_ = os.Remove(tmp)
		return fmt.Errorf("ativar %s: %w", exe, err)
	}
	_ = os.Chmod(exe, 0o755)
	if buf != nil {
		buf.Infof("update", "binário substituído: %s (backup %s)", exe, bak)
	}

	if trySystemdRestart(buf) {
		return nil
	}
	if tryPM2Restart(buf) {
		return nil
	}
	if buf != nil {
		buf.Infof("update", "sem systemd/PM2 — re-exec %s", exe)
	}
	return reexecSelf(exe)
}

func trySystemdRestart(buf *logx.Buffer) bool {
	systemctl, err := exec.LookPath("systemctl")
	if err != nil {
		return false
	}
	managed := exec.Command(systemctl, "is-active", "--quiet", systemdUnit).Run() == nil ||
		exec.Command(systemctl, "is-enabled", "--quiet", systemdUnit).Run() == nil
	if !managed {
		return false
	}
	cmd := exec.Command(systemctl, "restart", systemdUnit)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		if buf != nil {
			buf.Warnf("update", "systemctl restart: %v", err)
		}
		return false
	}
	go func() {
		time.Sleep(2 * time.Second)
		_ = cmd.Wait()
		os.Exit(0)
	}()
	if buf != nil {
		buf.Infof("update", "systemctl restart %s disparado", systemdUnit)
	}
	return true
}

func tryPM2Restart(buf *logx.Buffer) bool {
	pm2, err := exec.LookPath("pm2")
	if err != nil {
		return false
	}
	cmd := exec.Command(pm2, "restart", pm2AppName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		if buf != nil {
			buf.Warnf("update", "pm2 restart: %v", err)
		}
		return false
	}
	go func() {
		time.Sleep(2 * time.Second)
		_ = cmd.Wait()
		os.Exit(0)
	}()
	if buf != nil {
		buf.Infof("update", "pm2 restart %s disparado", pm2AppName)
	}
	return true
}

func reexecSelf(exe string) error {
	args := append([]string{exe}, os.Args[1:]...)
	env := os.Environ()
	return syscall.Exec(exe, args, env)
}

// PreferRegistryDeb informa se o update deve baixar .deb em vez do binário solto.
func PreferRegistryDeb() bool {
	return paths.IsRegistryDebInstall()
}
