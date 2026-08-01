//go:build !unix

package ca

func InstallFirefoxEnterpriseRoots(_ systemAppender) ([]string, error) {
	return nil, nil
}
