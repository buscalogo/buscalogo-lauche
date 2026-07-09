package update

import (
	"fmt"
	"os"
	"path/filepath"

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
	return nil
}
