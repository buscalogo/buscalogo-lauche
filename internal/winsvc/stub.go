//go:build !windows

package winsvc

import "fmt"

const ServiceName = "BuscaLogoAgent"

// Run is unavailable outside Windows.
func Run(name string, start func(stop <-chan struct{}) error) error {
	return fmt.Errorf("serviço Windows só está disponível no Windows")
}

// StartService is a no-op stub on non-Windows.
func StartService() error {
	return fmt.Errorf("serviço Windows só está disponível no Windows")
}
