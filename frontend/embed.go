package frontend

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed index.html app.js style.css
var content embed.FS

// FS expõe os arquivos do painel.
func FS() fs.FS {
	sub, err := fs.Sub(content, ".")
	if err != nil {
		return content
	}
	return sub
}

// Handler retorna um http.Handler que serve o painel web.
func Handler() http.Handler {
	return http.FileServer(http.FS(FS()))
}
