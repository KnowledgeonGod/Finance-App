package httpapi

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed templates
var frontendFiles embed.FS

var frontendTemplate = template.Must(template.ParseFS(frontendFiles, "templates/*.html"))

func serveFrontend(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := frontendTemplate.ExecuteTemplate(w, "index.html", nil); err != nil {
		http.Error(w, "unable to render page", http.StatusInternalServerError)
	}
}

func frontendAssets() http.Handler {
	assets, err := fs.Sub(frontendFiles, "templates/assets")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "frontend assets unavailable", http.StatusInternalServerError)
		})
	}
	return http.StripPrefix("/assets/", http.FileServer(http.FS(assets)))
}
