//go:build windows

package system

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// IsRoot indica se o processo corre elevado (Administrador / UAC elevated).
func IsRoot() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()

	var elevation uint32
	var outLen uint32
	err := windows.GetTokenInformation(
		token,
		windows.TokenElevation,
		(*byte)(unsafe.Pointer(&elevation)),
		uint32(unsafe.Sizeof(elevation)),
		&outLen,
	)
	if err == nil {
		return elevation != 0
	}

	// Fallback: membro do grupo Administrators.
	sid, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false
	}
	member, err := token.IsMember(sid)
	return err == nil && member
}

func logWindowsAdmin(buf appender, op string) {
	msg := fmt.Sprintf("[system] Windows: %s — rode como Administrador se necessário\n", op)
	if buf != nil {
		_, _ = buf.Write([]byte(msg))
	} else {
		fmt.Fprint(os.Stderr, msg)
	}
}

// SetCapNetBindService é no-op no Windows (capabilities Linux).
func SetCapNetBindService(buf appender, _ string) error {
	logWindowsAdmin(buf, "SetCapNetBindService")
	return nil
}

// SetCapNet é no-op no Windows (capabilities Linux).
func SetCapNet(buf appender, _ string) error {
	logWindowsAdmin(buf, "SetCapNet")
	return nil
}

// ClearCap é no-op no Windows.
func ClearCap(_ appender, _ string) error {
	return nil
}

// HasCap no Windows não existe; retorna string vazia.
func HasCap(_ string) (string, error) {
	return "", nil
}

// HasCapContains: true no Windows para não bloquear a UI de bind (sem setcap).
func HasCapContains(_, _ string) bool {
	return true
}
