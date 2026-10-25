package orbit

// Contract stub for milestone 5 (PLAN.md §4, §5b). The runtime agent replaces
// this file with the real implementation; the project agent generates
// orbit_gen/app against these signatures.

import "net/http"

// Site is a whole Orbit site, created by the generated app.New().
type Site struct{}

// Option configures a Site.
type Option func(*siteOptions)

type siteOptions struct{}

// Island is one entry of the server:defer island table (milestone 8).
// Generated code passes nil until then.
type Island struct{}

// NewSite is called by the generated orbit_gen/app.New().
func NewSite(routes []Route, islands []Island, opts ...Option) *Site {
	panic("orbit: NewSite not implemented yet")
}

// WithSite sets the absolute site URL exposed as ctx.Site. The generated
// app.New() passes orbit.json's "site" first, so a caller's option wins.
func WithSite(rawURL string) Option { return func(*siteOptions) {} }

// WithStaticDir sets the directory of built static files.
func WithStaticDir(dir string) Option { return func(*siteOptions) {} }

// Middleware serves Orbit's requests and passes the rest to next.
func (s *Site) Middleware(next http.Handler) http.Handler { panic("orbit: not implemented yet") }

// Handler is Middleware(NotFound()).
func (s *Site) Handler() http.Handler { panic("orbit: not implemented yet") }

// NotFound renders 404.orbit with a 404 status.
func (s *Site) NotFound() http.Handler { panic("orbit: not implemented yet") }

// Addr is $ORBIT_ADDR, else ":"+$PORT, else ":4321".
func Addr() string { panic("orbit: not implemented yet") }

// ListenAndServe listens on Addr() and shuts down gracefully on SIGINT/SIGTERM.
func ListenAndServe(h http.Handler) error { panic("orbit: not implemented yet") }
