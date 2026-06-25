package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// IsRoot informa se o processo atual roda como root.
func IsRoot() bool { return os.Geteuid() == 0 }

// HasCommand verifica se um binário existe no PATH.
func HasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// hasDisplay detecta se há uma sessão gráfica para pkexec.
func hasDisplay() bool {
	if os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" {
		return true
	}
	if t := os.Getenv("XDG_SESSION_TYPE"); t == "x11" || t == "wayland" {
		return true
	}
	return false
}

// RunPrivileged executa um comando exigindo privilégio.
// Tenta na ordem: root direto, sudo (CLI) e pkexec (GUI).
// Em servidores sem gráfico (headless), prefere sudo para evitar o erro de terminal.
// O comando tem um timeout de 60s para evitar travamento em prompts de senha pendurados.
func RunPrivileged(buf appender, name string, args ...string) ([]byte, error) {
	display := strings.Join(append([]string{name}, args...), " ")

	if IsRoot() {
		if buf != nil {
			fmt.Fprintf(buf, "[system] # %s\n", display)
		}
		return runWithTimeout(context.Background(), name, args...)
	}

	// Headless: sudo primeiro; GUI: pkexec primeiro (menos intrusivo).
	if hasDisplay() {
		if HasCommand("pkexec") {
			if buf != nil {
				fmt.Fprintf(buf, "[system] pkexec %s\n", display)
			}
			full := append([]string{name}, args...)
			return runWithTimeout(context.Background(), "pkexec", full...)
		}
	}
	if HasCommand("sudo") {
		if buf != nil {
			fmt.Fprintf(buf, "[system] sudo %s\n", display)
		}
		full := append([]string{"--", name}, args...)
		return runWithTimeout(context.Background(), "sudo", full...)
	}
	if !hasDisplay() && HasCommand("pkexec") {
		if buf != nil {
			fmt.Fprintf(buf, "[system] pkexec %s\n", display)
		}
		full := append([]string{name}, args...)
		return runWithTimeout(context.Background(), "pkexec", full...)
	}
	return nil, errors.New("nenhum método de elevação disponível (pkexec/sudo); rode o agente como root")
}

// runWithTimeout executa um comando com timeout de 60s.
// Previne que um prompt de senha pendurado bloqueie indefinidamente.
func runWithTimeout(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var out strings.Builder
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return []byte(out.String()), err
}

type appender interface {
	Write(p []byte) (int, error)
}

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
