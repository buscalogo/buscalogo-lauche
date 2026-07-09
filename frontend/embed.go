package frontend

import (
	"embed"
	"io/fs"
	"net/http"

	"buscalogo-agent/assets"
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
	mux := http.NewServeMux()
	mux.HandleFunc("/logo.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(assets.Logo())
	})
	mux.Handle("/", http.FileServer(http.FS(FS())))
	return mux
}
