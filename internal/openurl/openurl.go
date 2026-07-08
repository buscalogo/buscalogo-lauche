package openurl

import (
	"fmt"
	"net/url"
	"os/exec"
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

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", raw)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", raw)
	default:
		cmd = exec.Command("open", raw)
	}
	return cmd.Start()
}
