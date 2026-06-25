package assets

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

//go:embed all:linux
var linuxFS embed.FS

//go:embed icons/logo.png
var logoBytes []byte

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

// Has informa se o binário `name` está embutido para a plataforma atual.
func Has(name string) bool {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.ToSlash(filepath.Join(platformDir(), name))
	f, err := linuxFS.Open(p)
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
	entries, err := linuxFS.ReadDir(platformDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || e.Name() == "MANIFEST" || e.Name() == ".gitkeep" {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}

// embeddedBytes lê o binário embutido.
func embeddedBytes(name string) ([]byte, error) {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.ToSlash(filepath.Join(platformDir(), name))
	return linuxFS.ReadFile(p)
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	outName := name
	if runtime.GOOS == "windows" {
		outName += ".exe"
	}
	target := filepath.Join(dir, outName)
	sumFile := filepath.Join(dir, outName+".sha256")
	want := sum(data)

	if existing, err := os.ReadFile(sumFile); err == nil && string(existing) == want {
		if _, err := os.Stat(target); err == nil {
			return target, nil
		}
	}

	if err := os.WriteFile(target, data, 0o755); err != nil {
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
		if runtime.GOOS == "windows" {
			name = name[:len(name)-len(".exe")]
		}
		p, err := Ensure(name, dir)
		if err != nil {
			return result, err
		}
		result[name] = p
	}
	return result, nil
}

// FS expõe o FS embutido para usos especiais.
func FS() fs.FS { sub, _ := fs.Sub(linuxFS, platformDir()); return sub }
