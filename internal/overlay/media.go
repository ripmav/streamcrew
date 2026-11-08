// SPDX-License-Identifier: MIT

package overlay

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ripmav/streamcrew/internal/capability"
)

// mediaRoot is the URL root of the media directory of the profile (B6).
const mediaRoot = "media"

// serveMedia serves the released media: /media/<root>/<path...> with
// http.ServeContent, so range requests work (B6). The roots are the
// media directory of the profile and the released file roots. With a
// set token the URL requires it (401, B5); without the right host:fs
// the route serves nothing (404, B6). A path that escapes a root
// answers 404 as well, never 403 (B47).
func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request) {
	// The token check comes before everything else: an unauthenticated
	// request gets 401, whatever the core may serve (B5).
	if _, ok := s.authorized(r); !ok {
		s.refuse(r.Context(), w, r)
		return
	}
	if !s.cfg.Capabilities.Current().Has(capability.HostFS) {
		http.NotFound(w, r)
		return
	}
	rootName := r.PathValue("root")
	path := r.PathValue("path")

	var dir string
	switch rootName {
	case mediaRoot:
		dir = s.cfg.MediaDir
	default:
		if s.cfg.Root == nil {
			http.NotFound(w, r)
			return
		}
		d, ok := s.cfg.Root(rootName)
		if !ok {
			http.NotFound(w, r)
			return
		}
		dir = d
	}

	// The root is opened per request: the released roots change at
	// runtime (Code-ADR-0019), and a missing directory is a 404, not a
	// failure of the server. Files opened from the root stay usable
	// after it is closed, so the root itself goes on the stack.
	root, err := os.OpenRoot(dir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	// os.Root rejects absolute paths and every ".." that escapes the
	// root, so a traversal stays a 404 (B47).
	file, err := root.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	// ServeContent keeps a Content-Type set before the call; it
	// detects the type from the content otherwise. A known extension
	// gets its registered type (B6).
	if ct := mime.TypeByExtension(filepath.Ext(info.Name())); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}
