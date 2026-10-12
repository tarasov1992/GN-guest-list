package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
)

func (h *Handler) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()

		userHash := sha256.Sum256([]byte(user))
		passHash := sha256.Sum256([]byte(pass))
		wantUser := sha256.Sum256([]byte("admin"))
		wantPass := sha256.Sum256([]byte(h.adminPassword))

		userOK := subtle.ConstantTimeCompare(userHash[:], wantUser[:]) == 1
		passOK := subtle.ConstantTimeCompare(passHash[:], wantPass[:]) == 1

		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="admin", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// requireHTMX guards admin POSTs against cross-site requests: the browser remembers
// the admin's basic-auth credentials, but a form on another site can't set this
// header (that would need a CORS preflight, which we never allow).
func requireHTMX(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("HX-Request") != "true" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
