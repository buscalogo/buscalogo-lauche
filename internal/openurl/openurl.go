package openurl

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Open abre uma URL http(s) no navegador ou app padrão do sistema.
func Open(raw string) error {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("url inválida")
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("apenas http e https são permitidos")
	}
	if u.Host == "" {
		return fmt.Errorf("url inválida")
	}
	return startOpen(raw)
}

// OpenPath abre um arquivo ou pasta no gerenciador de arquivos padrão.
func OpenPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("caminho vazio")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("não é um diretório: %s", abs)
	}
	return startOpen(abs)
}

// OpenBrowserPage tenta abrir chrome:// ou about: no navegador correspondente.
func OpenBrowserPage(raw string) error {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("url inválida")
	}
	switch u.Scheme {
	case "chrome", "about":
	case "http", "https":
		return Open(raw)
	default:
		return fmt.Errorf("esquema não suportado: %s", u.Scheme)
	}

	if runtime.GOOS != "linux" {
		return startOpen(raw)
	}

	var bins []string
	switch u.Scheme {
	case "chrome":
		bins = []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "brave-browser", "microsoft-edge"}
	case "about":
		bins = []string{"firefox", "firefox-esr"}
	}
	for _, bin := range bins {
		if _, err := exec.LookPath(bin); err != nil {
			continue
		}
		cmd := exec.Command(bin, raw)
		if err := cmd.Start(); err == nil {
			return nil
		}
	}
	return startOpen(raw)
}

func startOpen(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("open", target)
	}
	return cmd.Start()
}
