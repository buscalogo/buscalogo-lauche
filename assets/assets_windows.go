//go:build windows

package assets

import "embed"

//go:embed icons/logo.png
var logoBytes []byte

// Núcleo embutido no build Windows. Preencha via scripts/fetch-windows-assets.sh.
//
//go:embed all:windows
var platformFS embed.FS
