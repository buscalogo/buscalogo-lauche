//go:build windows

package winsvc

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows/svc"
)

const ServiceName = "BuscaLogoAgent"

// Run registers with the Windows SCM and runs until Stop/Shutdown.
// start must block until stop is closed, then return after cleanup.
func Run(name string, start func(stop <-chan struct{}) error) error {
	if name == "" {
		name = ServiceName
	}
	return svc.Run(name, &handler{start: start})
}

type handler struct {
	start func(stop <-chan struct{}) error
}

func (h *handler) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const cmds = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	stop := make(chan struct{})
	errCh := make(chan error, 1)
	go func() { errCh <- h.start(stop) }()

	// Allow the agent a moment to bind ports before reporting running.
	select {
	case err := <-errCh:
		if err != nil {
			changes <- svc.Status{State: svc.StopPending}
			return true, 1
		}
		changes <- svc.Status{State: svc.StopPending}
		return false, 0
	case <-time.After(2 * time.Second):
	}

	changes <- svc.Status{State: svc.Running, Accepts: cmds}

	for {
		select {
		case err := <-errCh:
			changes <- svc.Status{State: svc.StopPending}
			if err != nil {
				return true, 1
			}
			return false, 0
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				close(stop)
				select {
				case <-errCh:
				case <-time.After(30 * time.Second):
				}
				return false, 0
			default:
				// ignore pause/continue
			}
		}
	}
}

// StartService asks the SCM to start BuscaLogoAgent (requires privilege).
func StartService() error {
	return startNamed(ServiceName)
}

func startNamed(name string) error {
	m, err := openMgr()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("serviço %s: %w", name, err)
	}
	defer s.Close()
	return s.Start()
}
