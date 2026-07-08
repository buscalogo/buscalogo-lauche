package paths

import (
	"os"
	"path/filepath"
	"strings"
)

const envHome = "BUSCALOGO_HOME"

// isSystemPath retorna true se o binário está em um diretório de sistema
// (ex: /usr, /opt, /bin), onde o usuário normal não tem permissão de escrita.
func isSystemPath(p string) bool {
	p = filepath.Clean(p)
	prefixes := []string{"/usr/", "/usr/local/", "/opt/", "/bin/", "/sbin/"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(p, prefix) || p == strings.TrimSuffix(prefix, "/") {
			return true
		}
	}
	return false
}

func isDaemonBinary(name string) bool {
	return name == "buscalogo-agentd" || strings.HasPrefix(name, "buscalogo-agentd")
}

func userDataHome() (string, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(userHome, ".buscalogo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func homeDir() (string, error) {
	if v := os.Getenv(envHome); v != "" {
		if err := os.MkdirAll(v, 0o755); err != nil {
			return "", err
		}
		return v, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	// Daemon e instalações em /opt usam ~/.buscalogo — evita paths longos
	// (ex.: unix socket do Yggdrasil) e problemas de permissão em /opt.
	if isSystemPath(exe) || isDaemonBinary(filepath.Base(exe)) {
		return userDataHome()
	}
	return filepath.Dir(exe), nil
}

// DaemonExecutable resolves buscalogo-agentd beside the current binary.
// Falls back to the current executable for legacy single-binary installs.
func DaemonExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	base := filepath.Base(exe)
	if isDaemonBinary(base) {
		return exe, nil
	}
	cand := filepath.Join(filepath.Dir(exe), "buscalogo-agentd")
	if st, err := os.Stat(cand); err == nil && !st.IsDir() {
		return cand, nil
	}
	return exe, nil
}

func sub(name string) (string, error) {
	base, err := homeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func Home() (string, error)  { return homeDir() }
func Data() (string, error)  { return sub("data") }
func Bin() (string, error)   { return sub(filepath.Join("data", "bin")) }
func Cache() (string, error) { return sub(filepath.Join("data", "cache")) }
func Logs() (string, error)  { return sub(filepath.Join("data", "logs")) }

func ConfigFile() (string, error) {
	d, err := Data()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.yaml"), nil
}

func SitesHostsFile() (string, error) {
	d, err := Data()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "sites.hosts"), nil
}

func Join(first string, more ...string) string {
	return filepath.Join(append([]string{first}, more...)...)
}
