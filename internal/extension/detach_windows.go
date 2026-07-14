//go:build windows

package extension

import "syscall"

const createNewProcessGroup = 0x00000200

func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}
