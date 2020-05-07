package fs

import (
	"net/http"
	"strings"
)

// Serve returns a Server wrapper with specified directory
// prefix, which can be used as http.Handler.
//
// Usage:
//     http.ListenAndServe(":80", br.Serve("public"))
//
func (br *Broccoli) Serve(dir string) http.Handler {
	srv := br.NewServer(dir)
	return http.FileServer(srv)
}

func (br *Broccoli) NewServer(dir string) *Server {
	srv := &Server{
		br:     br,
		prefix: strings.Trim(dir, "/"),
	}
	return srv
}

// Server implements a http.FileSystem and provides
// access to the Broccoli fs content by specified prefix.
type Server struct {
	br     *Broccoli
	prefix string
}

// Open opens the named file for reading. Filepath
// will be prepended with Server's prefix.
func (s *Server) Open(filepath string) (http.File, error) {
	return s.br.Open(s.prefix + filepath)
}
