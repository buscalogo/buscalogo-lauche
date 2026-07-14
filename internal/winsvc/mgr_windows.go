//go:build windows

package winsvc

import "golang.org/x/sys/windows/svc/mgr"

func openMgr() (*mgr.Mgr, error) {
	return mgr.Connect()
}
