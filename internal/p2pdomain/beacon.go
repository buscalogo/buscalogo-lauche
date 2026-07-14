package p2pdomain

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"time"
)

// Beacon UDP na LAN para Agents se descobrirem sem varrer seeds Ygg.
const (
	beaconPortOff   = 2 // 4401 → 4403
	beaconMulticast = "239.255.76.67"
)

type beaconMsg struct {
	V            int    `json:"v"`
	PeerID       string `json:"peer_id"`
	YggIP        string `json:"ygg_ip"`
	DiscoverPort int    `json:"discover_port"`
}

func (s *Service) beaconLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	s.sendBeacon()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sendBeacon()
		}
	}
}

func (s *Service) sendBeacon() {
	s.mu.Lock()
	h := s.host
	ygg := s.selfYgg
	port := s.port
	s.mu.Unlock()
	if h == nil || ygg == "" {
		return
	}
	payload, err := json.Marshal(beaconMsg{
		V: 1, PeerID: h.ID().String(), YggIP: ygg, DiscoverPort: port + discoverPortOff,
	})
	if err != nil {
		return
	}
	portStr := strconv.Itoa(port + beaconPortOff)
	addr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(beaconMulticast, portStr))
	if err != nil {
		return
	}
	c, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		return
	}
	defer c.Close()
	_, _ = c.Write(payload)
}

func (s *Service) listenBeacon(ctx context.Context) {
	s.mu.Lock()
	port := s.port
	s.mu.Unlock()
	portStr := strconv.Itoa(port + beaconPortOff)
	addr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(beaconMulticast, portStr))
	if err != nil {
		return
	}
	conn, err := net.ListenMulticastUDP("udp4", nil, addr)
	if err != nil {
		// Fallback: unicast listen (ainda recebe se alguém direcionar; multicast pode falhar em algumas ifs).
		conn, err = net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port + beaconPortOff})
		if err != nil {
			if s.buf != nil {
				s.buf.Warnf("p2pdomain", "beacon listen: %v", err)
			}
			return
		}
	}
	defer conn.Close()
	_ = conn.SetReadBuffer(1 << 16)
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	buf := make([]byte, 2048)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}
		var msg beaconMsg
		if json.Unmarshal(buf[:n], &msg) != nil || msg.V < 1 || msg.YggIP == "" {
			continue
		}
		s.mu.Lock()
		self := s.selfYgg
		hid := ""
		if s.host != nil {
			hid = s.host.ID().String()
		}
		s.mu.Unlock()
		if msg.PeerID != "" && msg.PeerID == hid {
			continue
		}
		ip := normalizeYggIP(msg.YggIP)
		if ip == "" || ip == self {
			continue
		}
		rememberAgent(ip, msg.PeerID, false)
		if !s.markBeaconAttempt(ip) {
			continue
		}
		// Conecta sem esperar o ticker periódico.
		go func(ip string) {
			cctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			pid, err := s.discoverAndConnect(cctx, ip)
			if err != nil {
				return
			}
			if _, err := s.pullSyncCounted(cctx, pid); err == nil {
				rememberAgent(ip, pid.String(), true)
			}
		}(ip)
	}
}

func (s *Service) markBeaconAttempt(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.beaconTried == nil {
		s.beaconTried = map[string]time.Time{}
	}
	if t, ok := s.beaconTried[ip]; ok && time.Since(t) < 20*time.Second {
		return false
	}
	s.beaconTried[ip] = time.Now()
	return true
}
