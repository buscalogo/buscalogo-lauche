//go:build !windows

package extension

import (
	"fmt"
	"os/exec"
)

func FindChromium() (string, error) {
	for _, bin := range chromiumBins {
		if p, err := exec.LookPath(bin); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("nenhum Chrome/Chromium/Edge/Brave encontrado no PATH")
}

func FindFirefox() (string, error) {
	for _, bin := range firefoxBins {
		if p, err := exec.LookPath(bin); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("Firefox não encontrado no PATH")
}
