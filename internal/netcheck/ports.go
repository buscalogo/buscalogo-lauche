package netcheck

import (
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PortReport descreve se uma porta crítica para a rede .bl está acessível.
type PortReport struct {
	Name            string `json:"name"`
	Port            int    `json:"port"`
	Proto           string `json:"proto"` // tcp | udp
	Purpose         string `json:"purpose"`
	Listening       bool   `json:"listening"`
	LocalhostOnly   bool   `json:"localhost_only,omitempty"`
	ReachableViaYgg bool   `json:"reachable_via_ygg"`
	OK              bool   `json:"ok"`
	Status          string `json:"status"` // ok | not_listening | localhost_only | firewall | unknown
	Hint            string `json:"hint,omitempty"`
}

// Summary agrega checagens para o Overview.
type Summary struct {
	YggIP       string       `json:"ygg_ip,omitempty"`
	CheckedAt   string       `json:"checked_at"`
	AllOK       bool         `json:"all_ok"`
	Blocked     []string     `json:"blocked"`
	Ports       []PortReport `json:"ports"`
	FirewallTip string       `json:"firewall_tip,omitempty"`
}

type checkSpec struct {
	name    string
	port    int
	proto   string
	purpose string
	yggDial bool
}

// CheckBLPorts verifica listen local + alcance via próprio IPv6 Ygg (firewall INPUT).
func CheckBLPorts(yggIP string, libp2pPort, webPort, tlsPort int) Summary {
	if libp2pPort <= 0 {
		libp2pPort = 4401
	}
	if webPort <= 0 {
		webPort = 80
	}
	if tlsPort <= 0 {
		tlsPort = 443
	}
	yggIP = strings.TrimSpace(strings.Trim(yggIP, "[]"))
	if i := strings.IndexByte(yggIP, '/'); i >= 0 {
		yggIP = yggIP[:i]
	}

	specs := []checkSpec{
		{name: "libp2p / gossip", port: libp2pPort, proto: "tcp", purpose: "GossipSub e sync de DNS .bl", yggDial: true},
		{name: "discover", port: libp2pPort + 1, proto: "tcp", purpose: "Outros Agents descobrem este nó na mesh Ygg", yggDial: true},
		{name: "beacon LAN", port: libp2pPort + 2, proto: "udp", purpose: "Descoberta só na LAN (multicast)", yggDial: false},
		{name: "HTTP sites .bl", port: webPort, proto: "tcp", purpose: "http://site.bl neste host", yggDial: true},
		{name: "HTTPS sites .bl", port: tlsPort, proto: "tcp", purpose: "https://site.bl (TLS — self-signed até CA)", yggDial: true},
	}

	out := make([]PortReport, len(specs))
	var wg sync.WaitGroup
	for i, sp := range specs {
		wg.Add(1)
		go func(i int, sp checkSpec) {
			defer wg.Done()
			out[i] = checkOne(sp, yggIP)
		}(i, sp)
	}
	wg.Wait()

	sum := Summary{
		YggIP:     yggIP,
		CheckedAt: time.Now().Format(time.RFC3339),
		Ports:     out,
		AllOK:     true,
	}
	for _, p := range out {
		if !p.OK {
			sum.AllOK = false
			sum.Blocked = append(sum.Blocked, fmt.Sprintf("%s :%d/%s", p.Name, p.Port, p.Proto))
		}
	}
	if !sum.AllOK {
		sum.FirewallTip = firewallTip(libp2pPort, webPort, tlsPort)
	}
	return sum
}

func firewallTip(libp2pPort, webPort, tlsPort int) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(
			`No Windows Firewall, permita entrada TCP %d/%d (gossip/discover) e %d/%d (HTTP/HTTPS) no adaptador Yggdrasil. PowerShell (Admin): New-NetFirewallRule -DisplayName "BuscaLogo Ygg" -Direction Inbound -Action Allow -Protocol TCP -LocalPort %d,%d,%d,%d`,
			libp2pPort, libp2pPort+1, webPort, tlsPort, libp2pPort, libp2pPort+1, webPort, tlsPort,
		)
	}
	return fmt.Sprintf(
		"Libere entrada TCP %d/%d (gossip/discover) e %d/%d (HTTP/HTTPS) na tun0. Ex.: sudo ufw allow in on tun0 to any port %d proto tcp && sudo ufw allow in on tun0 to any port %d proto tcp && sudo ufw allow in on tun0 to any port %d proto tcp && sudo ufw allow in on tun0 to any port %d proto tcp",
		libp2pPort, libp2pPort+1, webPort, tlsPort, libp2pPort, libp2pPort+1, webPort, tlsPort,
	)
}

func checkOne(sp checkSpec, yggIP string) PortReport {
	r := PortReport{
		Name:    sp.name,
		Port:    sp.port,
		Proto:   sp.proto,
		Purpose: sp.purpose,
	}
	listening, localOnly := procListening(sp.proto, sp.port)
	r.Listening = listening
	r.LocalhostOnly = localOnly

	if !listening {
		// fallback: dial local ou o próprio IPv6 Ygg (gossip ouve só em /ip6/<ygg>)
		if sp.proto == "tcp" {
			if canDial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(sp.port))) ||
				canDial("tcp", net.JoinHostPort("::1", strconv.Itoa(sp.port))) {
				r.Listening = true
				r.LocalhostOnly = true
				localOnly = true
			} else if yggIP != "" && canDial("tcp", net.JoinHostPort(yggIP, strconv.Itoa(sp.port))) {
				r.Listening = true
				r.LocalhostOnly = false
				r.ReachableViaYgg = true
				r.OK = true
				r.Status = "ok"
				return r
			} else {
				r.Status = "not_listening"
				r.Hint = "Serviço não está escutando nesta porta."
				return r
			}
		} else {
			r.Status = "not_listening"
			r.Hint = "Serviço não está escutando nesta porta."
			return r
		}
	}
	if localOnly {
		r.Status = "localhost_only"
		r.Hint = "Só escuta em 127.0.0.1 — peers na Ygg não alcançam."
		return r
	}

	if !sp.yggDial || yggIP == "" {
		r.OK = true
		r.Status = "ok"
		if yggIP == "" && sp.yggDial {
			r.Status = "unknown"
			r.OK = false
			r.Hint = "Sem IPv6 Ygg ainda — não dá para testar entrada pela mesh."
		}
		return r
	}

	ok := canDial("tcp", net.JoinHostPort(yggIP, strconv.Itoa(sp.port)))
	r.ReachableViaYgg = ok
	if ok {
		r.OK = true
		r.Status = "ok"
		return r
	}
	// No Windows o auto-teste via próprio IPv6 Ygg é pouco fiável (Firewall / TUN).
	if runtime.GOOS == "windows" && listening && !localOnly {
		r.OK = true
		r.Status = "ok"
		r.Hint = "Escuta não-local detectada; o auto-teste Ygg no Windows é pouco fiável. Se peers externos falharem, permita TCP no Firewall (adaptador Yggdrasil)."
		return r
	}
	iface := "tun0"
	if runtime.GOOS == "windows" {
		iface = "adaptador Yggdrasil"
	}
	r.Status = "firewall"
	r.Hint = fmt.Sprintf("Escuta local ok, mas conexão via IPv6 Ygg falhou — firewall provavelmente bloqueia entrada no %s.", iface)
	return r
}

func canDial(network, addr string) bool {
	c, err := net.DialTimeout(network, addr, 900*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

