package yggdrasil

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"
)

// AdminInfo agrega informações úteis do socket admin do Yggdrasil.
type AdminInfo struct {
	SocketExists bool        `json:"socket_exists"`
	Reachable    bool        `json:"reachable"`
	Self         *SelfInfo   `json:"self,omitempty"`
	Peers        []PeerInfo  `json:"peers"`
	Error        string      `json:"error,omitempty"`
}

type SelfInfo struct {
	Key      string   `json:"key"`
	Address  string   `json:"address"`
	Subnet   string   `json:"subnet"`
	Coords   []uint64 `json:"coords"`
	Build    string   `json:"build"`
	Version  string   `json:"version"`
}

type PeerInfo struct {
	Key      string `json:"key"`
	Address  string `json:"address,omitempty"`
	Endpoint string `json:"endpoint"`
	Port     uint64 `json:"port"`
	Priority uint64 `json:"priority"`
	UpTime   int64  `json:"uptime"`
	// Campos adicionais podem vir como mapa.
}

func (s *Service) adminSocket() string {
	if sock, err := adminSocketPath(); err == nil {
		return sock
	}
	return ""
}

// AdminInfo consulta o socket admin e retorna informações do nó e peers.
func (s *Service) AdminInfo() *AdminInfo {
	sock := s.adminSocket()
	info := &AdminInfo{SocketExists: false, Reachable: false, Peers: make([]PeerInfo, 0)}
	if sock == "" {
		info.Error = "caminho do socket admin não resolvido"
		return info
	}
	if _, err := os.Stat(sock); err != nil {
		if os.IsNotExist(err) {
			info.Error = "socket admin ainda não criado"
		} else {
			info.Error = err.Error()
		}
		return info
	}
	info.SocketExists = true

	conn, err := net.DialTimeout("unix", sock, 500*time.Millisecond)
	if err != nil {
		info.Error = fmt.Sprintf("conectar ao socket: %v", err)
		return info
	}
	defer conn.Close()
	info.Reachable = true
	_ = conn.Close()

	self, err := s.adminRequest(map[string]any{"request": "getSelf"})
	if err != nil {
		info.Error = err.Error()
		return info
	}
	info.Self = parseSelf(self)

	peers, err := s.adminRequest(map[string]any{"request": "getPeers"})
	if err != nil {
		info.Error = err.Error()
		return info
	}
	info.Peers = parsePeers(peers)
	return info
}

func (s *Service) adminRequest(req map[string]any) (map[string]any, error) {
	sock := s.adminSocket()
	conn, err := net.DialTimeout("unix", sock, 500*time.Millisecond)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp map[string]any
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, err
	}
	if status, ok := resp["status"].(string); ok && status == "error" {
		errMsg, _ := resp["error"].(string)
		return nil, fmt.Errorf("admin error: %s", errMsg)
	}
	return resp, nil
}

func parseSelf(m map[string]any) *SelfInfo {
	s := &SelfInfo{}
	resp, ok := m["response"].(map[string]any)
	if !ok {
		return s
	}
	s.Key = getString(resp, "key")
	s.Address = getString(resp, "address")
	s.Subnet = getString(resp, "subnet")
	s.Build = getString(resp, "build_name")
	s.Version = getString(resp, "build_version")
	if coords, ok := resp["coords"].([]any); ok {
		for _, c := range coords {
			if n, ok := c.(float64); ok {
				s.Coords = append(s.Coords, uint64(n))
			}
		}
	}
	return s
}

func parsePeers(m map[string]any) []PeerInfo {
	peers := make([]PeerInfo, 0)
	resp, ok := m["response"].(map[string]any)
	if !ok {
		return peers
	}
	// Yggdrasil 0.5.x retorna "peers" como array.
	if list, ok := resp["peers"].([]any); ok {
		for _, v := range list {
			if p, ok := v.(map[string]any); ok {
				up, _ := p["up"].(bool)
				if !up {
					continue
				}
				peer := PeerInfo{
					Key:      getString(p, "key"),
					Address:  getString(p, "address"),
					Endpoint: getString(p, "remote"),
					Port:     getUint64(p, "port"),
					Priority: getUint64(p, "priority"),
					UpTime:   getInt64(p, "uptime"),
				}
				peers = append(peers, peer)
			}
		}
		return peers
	}
	// Versões antigas usam map.
	if list, ok := resp["peers"].(map[string]any); ok {
		for _, v := range list {
			if p, ok := v.(map[string]any); ok {
				peer := PeerInfo{
					Key:      getString(p, "key"),
					Endpoint: getString(p, "endpoint"),
					Port:     getUint64(p, "port"),
					Priority: getUint64(p, "priority"),
					UpTime:   getInt64(p, "uptime"),
				}
				peers = append(peers, peer)
			}
		}
	}
	return peers
}

func getString(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func getUint64(m map[string]any, k string) uint64 {
	if v, ok := m[k].(float64); ok {
		return uint64(v)
	}
	return 0
}

func getInt64(m map[string]any, k string) int64 {
	if v, ok := m[k].(float64); ok {
		return int64(v)
	}
	return 0
}
