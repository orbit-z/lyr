package route_test

import (
	"strings"
	"testing"

	"github.com/orbit-z/lyr"
	"github.com/orbit-z/lyr/lyrtest"

	"example.com/site/lyr_gen/app"
	"example.com/site/lyr_gen/decl/pages/route/id_param"
	"example.com/site/src/locals"
)

// Get fills the RouteInfo field of Locals like a real request, both the
// locals passed with WithLocals and the ones a page allocates.
func TestGetFillsRoute(t *testing.T) {
	h := app.New().Handler()
	var l locals.Locals
	lyrtest.Get(t, h, "/blog/hello", lyrtest.WithLocals(&l))
	if l.Route.Kind != lyr.RoutePage || l.Route.Pattern != "/blog/[slug]" || l.Route.Param("slug") != "hello" ||
		l.Route.File != "src/pages/blog/[slug].lyr" {
		t.Errorf("route %+v", l.Route)
	}
	rec := lyrtest.Get(t, h, "/route/x%20y")
	if want := "<p>route=/route/[id] id=x y file=src/pages/route/[id].lyr</p>"; !strings.Contains(rec.Body.String(), want) {
		t.Errorf("GET /route/x%%20y: %q, want %q", rec.Body, want)
	}
	var nf locals.Locals
	lyrtest.Get(t, h, "/nope", lyrtest.WithLocals(&nf))
	if nf.Route.Kind != lyr.RouteNotFound || nf.Route.Pattern != "/404" {
		t.Errorf("404 route %+v", nf.Route)
	}
}

// Render has no route: the field stays zero.
func TestRenderNoRoute(t *testing.T) {
	html := lyrtest.Render(t, id_param.Render, id_param.Props{}, lyrtest.WithParam("id", "x"))
	if !strings.Contains(html, "<p>route= id= file=</p>") {
		t.Errorf("Render: %q", html)
	}
}
