package blcrypto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// SignBytes constrói o payload canônico (sem signature) para Ed25519.
// Layout length-prefixed: type, domain, owner, target, aaaa..., a..., txt..., nonce, timestamp.
func SignBytes(typ byte, domain string, owner, target []byte, aaaa, a, txt []string, nonce uint64, timestamp int64) []byte {
	var b bytes.Buffer
	writeByte(&b, typ)
	writeBytes(&b, []byte(domain))
	writeBytes(&b, owner)
	writeBytes(&b, target)
	writeStringSlice(&b, aaaa)
	writeStringSlice(&b, a)
	writeStringSlice(&b, txt)
	_ = binary.Write(&b, binary.BigEndian, nonce)
	_ = binary.Write(&b, binary.BigEndian, timestamp)
	return b.Bytes()
}

func writeByte(b *bytes.Buffer, v byte) { b.WriteByte(v) }

func writeBytes(b *bytes.Buffer, v []byte) {
	_ = binary.Write(b, binary.BigEndian, uint32(len(v)))
	b.Write(v)
}

func writeStringSlice(b *bytes.Buffer, ss []string) {
	_ = binary.Write(b, binary.BigEndian, uint32(len(ss)))
	for _, s := range ss {
		writeBytes(b, []byte(s))
	}
}

// Sign assina o payload canônico com a chave privada Ed25519.
func Sign(priv ed25519.PrivateKey, payload []byte) ([]byte, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("chave privada inválida")
	}
	return ed25519.Sign(priv, payload), nil
}

// Verify verifica a assinatura Ed25519.
func Verify(pub, payload, sig []byte) bool {
	if len(pub) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), payload, sig)
}

// Hash retorna SHA-256 do blob (usado como event_hash / message id).
func Hash(raw []byte) []byte {
	sum := sha256.Sum256(raw)
	return sum[:]
}
