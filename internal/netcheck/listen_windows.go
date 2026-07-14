//go:build windows

package netcheck

import (
	"net"
	"os/exec"
	"strconv"
	"strings"
)

// procListening usa netstat para distinguir bind em 0.0.0.0, [::] ou Ygg
// vs só loopback. A heurística antiga (dial 127.0.0.1 → sempre localhost_only)
// marcava portas abertas como "Só local" e falhava em serviços só IPv6 Ygg.
func procListening(proto string, port int) (listening, localhostOnly bool) {
	proto = strings.ToLower(strings.TrimSpace(proto))
	out, err := exec.Command("netstat", "-ano", "-p", proto).CombinedOutput()
	if err != nil {
		if proto == "tcp" {
			addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
			if canDial("tcp", addr) {
				return true, true
			}
		}
		return false, false
	}
	wantSuffix := ":" + strconv.Itoa(port)
	seenLocal, seenReachable := false, false
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		if proto == "tcp" && !strings.Contains(upper, "LISTENING") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		local := fields[1]
		if !strings.HasSuffix(local, wantSuffix) {
			continue
		}
		host := localHost(local)
		if isLoopbackHost(host) {
			seenLocal = true
		} else {
			seenReachable = true
		}
	}
	if seenReachable {
		return true, false
	}
	if seenLocal {
		return true, true
	}
	return false, false
}

func localHost(local string) string {
	local = strings.TrimSpace(local)
	if strings.HasPrefix(local, "[") {
		if i := strings.Index(local, "]:"); i >= 0 {
			return local[1:i]
		}
		return strings.Trim(local, "[]")
	}
	if i := strings.LastIndex(local, ":"); i >= 0 {
		return local[:i]
	}
	return local
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
