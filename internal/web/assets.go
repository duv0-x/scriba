package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"sync"
)

// assetHashes: hash corto del contenido de cada archivo embebido en static/, calculado una vez.
var assetHashes = sync.OnceValue(func() map[string]string {
	m := map[string]string{}
	static, _ := fs.Sub(staticFS, "static")
	_ = fs.WalkDir(static, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(static, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		m[p] = hex.EncodeToString(sum[:5])
		return nil
	})
	return m
})

// asset devuelve la URL versionada de un estático: cambia con su contenido, así que se cachea para siempre.
func asset(name string) string {
	return "/static/" + name + "?v=" + assetHashes()[name]
}

// serveStatic: con la versión vigente en la URL, caché inmutable; sin ella (p. ej. fuentes pedidas desde
// app.css), revalidación con ETag para que un despliegue nunca deje CSS o JS viejos.
func serveStatic(files http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := assetHashes()[r.PathValue("file")]
		if h != "" {
			w.Header().Set("ETag", `"`+h+`"`)
		}
		if v := r.URL.Query().Get("v"); h != "" && v == h {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	}
}
