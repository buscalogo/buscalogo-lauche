package autostart

import (
	"fmt"
	"os"
	"path/filepath"

	"buscalogo-agent/internal/logx"
)

const desktopEntryName = "buscalogo-agent.desktop"

// IsEnabled verifica se o arquivo .desktop de autostart existe.
func IsEnabled() bool {
	path, err := desktopPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// Enable cria o arquivo .desktop de autostart para a sessão do usuário.
func Enable(buf *logx.Buffer) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("caminho do executável: %w", err)
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}
	exeDir := filepath.Dir(exe)

	dir, err := autostartDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	iconPath := findIcon(exeDir)

	path := filepath.Join(dir, desktopEntryName)
	content := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=BuscaLogo Agent
Exec=%s
Terminal=false
Icon=%s
Comment=BuscaLogo Agent — launcher automático
X-GNOME-Autostart-enabled=true
`, exe, iconPath)

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	if buf != nil {
		buf.Infof("autostart", "entrada de autostart criada em %s", path)
	}
	return nil
}

// findIcon localiza o arquivo PNG do logo junto ao binário, ou usa o nome
// do ícone de sistema "buscalogo-agent" como fallback.
func findIcon(exeDir string) string {
	candidates := []string{
		filepath.Join(exeDir, "buscalogo-agent.png"),
		filepath.Join(exeDir, "..", "share", "icons", "buscalogo-agent.png"),
		"/opt/buscalogo/buscalogo-agent.png",
		"/usr/share/icons/buscalogo-agent.png",
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return "buscalogo-agent"
}

// Disable remove o arquivo .desktop de autostart.
func Disable(buf *logx.Buffer) error {
	path, err := desktopPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if buf != nil {
		buf.Infof("autostart", "entrada de autostart removida")
	}
	return nil
}

func autostartDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "autostart"), nil
}

func desktopPath() (string, error) {
	dir, err := autostartDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, desktopEntryName), nil
}
