package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func verifyDeb(path, wantSHA string) error {
	wantSHA = strings.ToLower(strings.TrimSpace(wantSHA))
	if wantSHA == "" {
		return fmt.Errorf("manifest sem sha256 do pacote")
	}
	got, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if got != wantSHA {
		return fmt.Errorf("sha256 não confere (esperado %s, obtido %s)", wantSHA[:12]+"...", got[:12]+"...")
	}
	return nil
}
