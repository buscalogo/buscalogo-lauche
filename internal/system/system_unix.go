//go:build unix

package system

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// IsRoot informa se o processo atual roda como root.
func IsRoot() bool { return os.Geteuid() == 0 }

// SetCapNetBindService concede CAP_NET_BIND_SERVICE ao binário (bind de portas < 1024).
func SetCapNetBindService(buf appender, binary string) error {
	out, err := RunPrivileged(buf, "setcap", "cap_net_bind_service=+ep", binary)
	if err != nil {
		return fmt.Errorf("setcap cap_net_bind_service: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SetCapNet concede CAP_NET_ADMIN e CAP_NET_RAW ao binário (criar TUN — Yggdrasil).
func SetCapNet(buf appender, binary string) error {
	out, err := RunPrivileged(buf, "setcap", "cap_net_admin,cap_net_raw=+ep", binary)
	if err != nil {
		return fmt.Errorf("setcap cap_net_admin: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ClearCap remove as capabilities do binário.
func ClearCap(buf appender, binary string) error {
	out, err := RunPrivileged(buf, "setcap", "-r", binary)
	if err != nil {
		return fmt.Errorf("setcap -r: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// HasCap consulta as capabilities atuais do binário.
func HasCap(binary string) (string, error) {
	out, err := exec.Command("getcap", binary).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// HasCapContains verifica se o binário já possui a capability informada.
func HasCapContains(binary, cap string) bool {
	out, err := HasCap(binary)
	if err != nil {
		return false
	}
	return strings.Contains(out, cap)
}
