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
