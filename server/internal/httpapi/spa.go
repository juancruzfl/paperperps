package httpapi

import (
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
)

func init() {
	// Go's table doesn't know the web app manifest (web/public/site.webmanifest).
	if err := mime.AddExtensionType(".webmanifest", "application/manifest+json"); err != nil {
		panic(err)
	}
}

// spaHandler serves files from the built frontend in dir. Unknown paths get index.html so
// client-side routes like /restore work on reload.
func spaHandler(dir string) http.Handler {
	if dir == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "frontend not built: run `npm run dev` in web/ or set WEB_DIR", http.StatusNotFound)
		})
	}
	root := os.DirFS(dir)
	files := http.FileServerFS(root)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if info, err := fs.Stat(root, name); err != nil || info.IsDir() {
			name = "index.html"
		}

		if name == "index.html" {
			// Always revalidate so a deploy shows up immediately.
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, root, "index.html")
			return
		}
		if strings.HasPrefix(name, "assets/") {
			// Vite puts a content hash in every file name under assets/.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}
