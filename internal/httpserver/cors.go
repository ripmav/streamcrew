// SPDX-License-Identifier: MIT

package httpserver

import (
	"net/http"
)

// CrossOriginProtection guards a handler against cross-origin requests
// (roadmap 6.2.2): a request with an Origin header is only served if
// allow(origin) reports true, and the preflight of an allowed origin is
// answered directly. Requests without an Origin header (same origin, CLI,
// curl) pass through unchanged.
func CrossOriginProtection(allow func(origin string) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !allow(origin) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("cross-origin request refused\n"))
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers",
				"Authorization, Content-Type, Connect-Protocol-Version, Connect-Content-Encoding, Connect-Accept-Encoding, Grpc-Timeout, Grpc-Message, Grpc-Status, Grpc-Status-Details-bin")
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
