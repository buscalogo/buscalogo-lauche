package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Logo retorna os bytes da logo em PNG (32x32 ou 64x64) para o systray.
func Logo() []byte { return logoBytes }

// platformDir retorna o subdiretório do binário conforme o OS.
func platformDir() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	default:
		return "linux"
	}
}

// platformFileName mapeia nome lógico → ficheiro embutido.
// Em Windows, "yggdrasil" → "yggdrasil.exe"; nomes com extensão (.dll/.exe) ficam iguais.
func platformFileName(name string) string {
	if runtime.GOOS != "windows" {
		return name
	}
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".dll") {
		return name
	}
	return name + ".exe"
}

// Has informa se o binário `name` está embutido para a plataforma atual.
func Has(name string) bool {
	p := filepath.ToSlash(filepath.Join(platformDir(), platformFileName(name)))
	f, err := platformFS.Open(p)
	if err != nil {
		return false
	}
	stat, err := f.Stat()
	f.Close()
	if err != nil {
		return false
	}
	return !stat.IsDir() && stat.Size() > 0
}

// List retorna os nomes de binários embutidos válidos para a plataforma atual.
func List() []string {
	entries, err := platformFS.ReadDir(platformDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || e.Name() == "MANIFEST" || e.Name() == ".gitkeep" {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Size() == 0 {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}

// embeddedBytes lê o binário embutido.
func embeddedBytes(name string) ([]byte, error) {
	p := filepath.ToSlash(filepath.Join(platformDir(), platformFileName(name)))
	return platformFS.ReadFile(p)
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Ensure extrai o binário `name` para `dir` se ausente ou com checksum diferente.
// Retorna o caminho completo do binário no disco.
func Ensure(name, dir string) (string, error) {
	data, err := embeddedBytes(name)
	if err != nil {
		return "", fmt.Errorf("binário %s não embutido: %w", name, err)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("binário %s não embutido (placeholder vazio)", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	outName := platformFileName(name)
	target := filepath.Join(dir, outName)
	sumFile := filepath.Join(dir, outName+".sha256")
	want := sum(data)

	if existing, err := os.ReadFile(sumFile); err == nil && string(existing) == want {
		if _, err := os.Stat(target); err == nil {
			return target, nil
		}
	}

	mode := os.FileMode(0o755)
	if strings.HasSuffix(strings.ToLower(outName), ".dll") {
		mode = 0o644
	}
	if err := os.WriteFile(target, data, mode); err != nil {
		return "", err
	}
	if err := os.WriteFile(sumFile, []byte(want), 0o644); err != nil {
		return "", err
	}
	return target, nil
}

// EnsureAll extrai todos os binários embutidos para `dir`.
func EnsureAll(dir string) (map[string]string, error) {
	result := make(map[string]string)
	for _, name := range List() {
		logical := name
		if runtime.GOOS == "windows" && strings.HasSuffix(strings.ToLower(name), ".exe") {
			logical = strings.TrimSuffix(name, ".exe")
			logical = strings.TrimSuffix(logical, ".EXE")
		}
		p, err := Ensure(logical, dir)
		if err != nil {
			return result, err
		}
		result[logical] = p
	}
	return result, nil
}

// FS expõe o FS embutido para usos especiais.
func FS() fs.FS { sub, _ := fs.Sub(platformFS, platformDir()); return sub }
