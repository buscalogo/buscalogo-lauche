package extension

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"buscalogo-agent/internal/paths"
)

// ChromeWebStoreURL é a ficha pública da extensão BuscaLogo Agent.
const ChromeWebStoreURL = "https://chromewebstore.google.com/detail/buscalogo-agent/gecmkbanhikgnhpcdibplcfndapclneh"

var chromiumBins = []string{
	"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
	"brave-browser", "microsoft-edge", "microsoft-edge-stable",
}

var firefoxBins = []string{"firefox", "firefox-esr"}

// DirReady reports whether dir contains a valid unpacked extension.
func DirReady(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "manifest.json"))
	return err == nil && !info.IsDir()
}

// FindChromium returns the first available Chromium-based browser binary.
func FindChromium() (string, error) {
	for _, bin := range chromiumBins {
		if p, err := exec.LookPath(bin); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("nenhum Chrome/Chromium/Edge/Brave encontrado no PATH")
}

// FindFirefox returns the first available Firefox binary.
func FindFirefox() (string, error) {
	for _, bin := range firefoxBins {
		if p, err := exec.LookPath(bin); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("Firefox não encontrado no PATH")
}

// OpenChromeWebStore abre o Chrome/Chromium no perfil normal, na ficha da extensão.
func OpenChromeWebStore() (bin string, err error) {
	return OpenURL(ChromeWebStoreURL)
}

// OpenURL abre uma URL no Chromium (preferido) ou via xdg-open.
func OpenURL(rawURL string) (bin string, err error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("URL vazia")
	}
	if bin, err = FindChromium(); err == nil {
		cmd := exec.Command(bin, rawURL)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return bin, err
		}
		return bin, nil
	}
	if _, lookErr := exec.LookPath("xdg-open"); lookErr != nil {
		return "", fmt.Errorf("%v (e xdg-open indisponível)", err)
	}
	cmd := exec.Command("xdg-open", rawURL)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return "xdg-open", err
	}
	return "xdg-open", nil
}

func profileDir(browser string) (string, error) {
	home, err := paths.Home()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "browser-profiles", browser)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// LaunchChrome abre Chromium com a extensão já carregada (1 clique, sem loja).
// Usa um perfil dedicado em ~/.buscalogo/browser-profiles/chrome.
func LaunchChrome(extDir string) (bin string, profile string, err error) {
	if !DirReady(extDir) {
		return "", "", fmt.Errorf("extensão Chrome não encontrada em %s", extDir)
	}
	bin, err = FindChromium()
	if err != nil {
		return "", "", err
	}
	profile, err = profileDir("chrome")
	if err != nil {
		return "", "", err
	}
	args := []string{
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions-except=" + extDir,
		"--load-extension=" + extDir,
		"http://127.0.0.1:9970",
	}
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return bin, profile, err
	}
	return bin, profile, nil
}

// LaunchFirefoxGuide abre o Firefox na página de debug e copia o caminho da extensão.
func LaunchFirefoxGuide(extDir string) (bin string, copiedPath bool, err error) {
	if !DirReady(extDir) {
		return "", false, fmt.Errorf("extensão Firefox não encontrada em %s", extDir)
	}
	bin, err = FindFirefox()
	if err != nil {
		return "", false, err
	}
	copiedPath = copyToClipboard(extDir)
	cmd := exec.Command(bin, "about:debugging#/runtime/this-firefox")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return bin, copiedPath, err
	}
	_ = openFileManager(extDir)
	return bin, copiedPath, nil
}

// EnsureChromeDesktopShortcut cria atalho no menu de aplicativos.
func EnsureChromeDesktopShortcut(extDir string) (string, error) {
	if !DirReady(extDir) {
		return "", fmt.Errorf("extensão Chrome não encontrada em %s", extDir)
	}
	home, err := paths.Home()
	if err != nil {
		return "", err
	}
	bin, err := FindChromium()
	if err != nil {
		return "", err
	}
	profile, err := profileDir("chrome")
	if err != nil {
		return "", err
	}
	scriptDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		return "", err
	}
	scriptPath := filepath.Join(scriptDir, "buscalogo-chrome.sh")
	script := fmt.Sprintf(`#!/bin/sh
exec %q \
  --user-data-dir=%q \
  --no-first-run \
  --no-default-browser-check \
  --disable-extensions-except=%q \
  --load-extension=%q \
  "$@"
`, bin, profile, extDir, extDir)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return "", err
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return scriptPath, nil
	}
	apps := filepath.Join(userHome, ".local", "share", "applications")
	_ = os.MkdirAll(apps, 0o755)
	desktopPath := filepath.Join(apps, "buscalogo-chrome.desktop")
	icon := "/opt/buscalogo/buscalogo-agent.png"
	if _, err := os.Stat(icon); err != nil {
		icon = "web-browser"
	}
	desktop := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=BuscaLogo no Chrome
Comment=Chrome com a extensão BuscaLogo Agent já carregada
Exec=%s
Icon=%s
Terminal=false
Categories=Network;WebBrowser;
StartupNotify=true
`, scriptPath, icon)
	if err := os.WriteFile(desktopPath, []byte(desktop), 0o644); err != nil {
		return scriptPath, err
	}
	_ = exec.Command("update-desktop-database", apps).Run()
	return desktopPath, nil
}

func copyToClipboard(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, attempt := range []struct {
		bin  string
		args []string
	}{
		{"wl-copy", nil},
		{"xclip", []string{"-selection", "clipboard"}},
		{"xsel", []string{"--clipboard", "--input"}},
	} {
		if _, err := exec.LookPath(attempt.bin); err != nil {
			continue
		}
		cmd := exec.Command(attempt.bin, attempt.args...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return true
		}
	}
	return false
}

func openFileManager(dir string) error {
	cmd := exec.Command("xdg-open", dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
