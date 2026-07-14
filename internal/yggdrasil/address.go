package yggdrasil

import (
	"crypto/ed25519"
	"encoding/hex"
	"net"
)

// AddrForKeyHex deriva o IPv6 Yggdrasil a partir da chave pública hex do peer.
func AddrForKeyHex(keyHex string) string {
	raw, err := hex.DecodeString(keyHex)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return ""
	}
	addr := addrForKey(ed25519.PublicKey(raw))
	if addr == nil {
		return ""
	}
	return net.IP(addr[:]).String()
}

// addrForKey espelha yggdrasil-go/src/address.AddrForKey (v0.5.x).
func addrForKey(publicKey ed25519.PublicKey) *[16]byte {
	if len(publicKey) != ed25519.PublicKeySize {
		return nil
	}
	var buf [ed25519.PublicKeySize]byte
	copy(buf[:], publicKey)
	for idx := range buf {
		buf[idx] = ^buf[idx]
	}
	var addr [16]byte
	temp := make([]byte, 0, 32)
	done := false
	ones := byte(0)
	bits := byte(0)
	nBits := 0
	for idx := 0; idx < 8*len(buf); idx++ {
		bit := (buf[idx/8] & (0x80 >> byte(idx%8))) >> byte(7-(idx%8))
		if !done && bit != 0 {
			ones++
			continue
		}
		if !done && bit == 0 {
			done = true
			continue
		}
		bits = (bits << 1) | bit
		nBits++
		if nBits == 8 {
			nBits = 0
			temp = append(temp, bits)
		}
	}
	prefix := [1]byte{0x02}
	copy(addr[:], prefix[:])
	addr[len(prefix)] = ones
	copy(addr[len(prefix)+1:], temp)
	return &addr
}
