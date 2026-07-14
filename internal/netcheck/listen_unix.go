//go:build unix

package netcheck

import (
	"os"
	"strconv"
	"strings"
)

func procListening(proto string, port int) (listening, localhostOnly bool) {
	files := []string{}
	if proto == "tcp" {
		files = []string{"/proc/net/tcp", "/proc/net/tcp6"}
	} else {
		files = []string{"/proc/net/udp", "/proc/net/udp6"}
	}
	seenLocal, seenReachable := false, false
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) < 4 {
				continue
			}
			state := fields[3]
			if proto == "tcp" && state != "0A" { // LISTEN
				continue
			}
			if proto == "udp" && state != "07" { // UDP "listening"
				continue
			}
			parts := strings.Split(fields[1], ":")
			if len(parts) != 2 {
				continue
			}
			p, err := strconv.ParseUint(parts[1], 16, 16)
			if err != nil || int(p) != port {
				continue
			}
			ipHex := strings.ToLower(parts[0])
			if isLoopbackHex(ipHex) {
				seenLocal = true
			} else {
				seenReachable = true
			}
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

func isLoopbackHex(ipHex string) bool {
	// IPv4 little-endian in /proc: 127.0.0.1 → 0100007F
	if len(ipHex) == 8 {
		return ipHex == "0100007f"
	}
	// IPv6 ::1
	return ipHex == "00000000000000000000000001000000"
}
