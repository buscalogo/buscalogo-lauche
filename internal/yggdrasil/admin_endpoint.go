package yggdrasil

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"time"
)

// Porta TCP local do admin no Windows (evita unix sockets e conflito com Ygg do sistema em :9001).
const adminTCPPort = 9901

// adminListenURI valor de AdminListen na config do Yggdrasil.
// Linux/macOS: unix socket. Windows: TCP em loopback (unix://C:\… quebra o parser).
func adminListenURI() (string, error) {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("tcp://127.0.0.1:%d", adminTCPPort), nil
	}
	sock, err := adminSocketPath()
	if err != nil {
		return "", err
	}
	return "unix://" + sock, nil
}

func adminDial(timeout time.Duration) (net.Conn, error) {
	uri, err := adminListenURI()
	if err != nil {
		return nil, err
	}
	switch {
	case strings.HasPrefix(uri, "tcp://"):
		return net.DialTimeout("tcp", strings.TrimPrefix(uri, "tcp://"), timeout)
	case strings.HasPrefix(uri, "unix://"):
		return net.DialTimeout("unix", strings.TrimPrefix(uri, "unix://"), timeout)
	default:
		return nil, fmt.Errorf("AdminListen não suportado: %s", uri)
	}
}

func adminEndpointReady() (exists bool, errMsg string) {
	uri, err := adminListenURI()
	if err != nil {
		return false, "endpoint admin não resolvido"
	}
	if strings.HasPrefix(uri, "tcp://") {
		conn, err := adminDial(200 * time.Millisecond)
		if err != nil {
			return false, "admin TCP ainda não responde"
		}
		_ = conn.Close()
		return true, ""
	}
	sock := strings.TrimPrefix(uri, "unix://")
	if _, err := os.Stat(sock); err != nil {
		if os.IsNotExist(err) {
			return false, "socket admin ainda não criado"
		}
		return false, err.Error()
	}
	return true, ""
}
