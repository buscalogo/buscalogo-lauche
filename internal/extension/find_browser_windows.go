//go:build windows

package extension

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func FindChromium() (string, error) {
	candidates := []string{}
	for _, root := range []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("LocalAppData"),
	} {
		if root == "" {
			continue
		}
		candidates = append(candidates,
			filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(root, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
		)
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	for _, bin := range []string{"chrome.exe", "msedge.exe", "brave.exe", "chromium.exe"} {
		if p, err := exec.LookPath(bin); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("nenhum Chrome/Edge/Brave encontrado")
}

func FindFirefox() (string, error) {
	candidates := []string{}
	for _, root := range []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("LocalAppData"),
	} {
		if root == "" {
			continue
		}
		candidates = append(candidates, filepath.Join(root, "Mozilla Firefox", "firefox.exe"))
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	if p, err := exec.LookPath("firefox.exe"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("Firefox não encontrado")
}
