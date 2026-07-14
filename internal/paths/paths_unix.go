//go:build !windows

package paths

func machineInstallDataDir(exe string) (string, bool) {
	return "", false
}

// IsProgramFilesInstall is always false outside Windows.
func IsProgramFilesInstall() bool { return false }
