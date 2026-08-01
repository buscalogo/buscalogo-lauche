//go:build windows

package ca

import "fmt"

type systemAppender interface {
	Write(p []byte) (int, error)
}

func SystemTrustInstalled() bool { return false }

func InstallSystemTrust(buf systemAppender, rootPEM []byte) error {
	return fmt.Errorf("instalação automática da CA no Windows ainda não suportada — importe rootCA.pem manualmente no repositório Root")
}

func UninstallSystemTrust(buf systemAppender) error {
	return fmt.Errorf("remoção automática da CA no Windows ainda não suportada")
}

func EnsureRootFile(destPath string, rootPEM []byte) error {
	return WriteRootFile(destPath, rootPEM)
}
