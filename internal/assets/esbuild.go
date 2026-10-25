package assets

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/evanw/esbuild/pkg/api"
)

// esbuildBundler is the Bundler built on esbuild's Go API. It keeps one
// incremental build context, reused by Bundle while the set of entries
// stays the same; the plugin reads the current plan, so a changed <style>
// or <script> only needs a Rebuild.
type esbuildBundler struct {
	opts   Options
	outdir string // virtual output directory: <root>/_orbit (nothing is written)

	mu   sync.RWMutex
	plan *plan // read by the plugin during a build

	ctx      api.BuildContext
	ctxKey   string
	contexts int // contexts created, for tests
}

// New returns the esbuild Bundler.
func New(opts Options) Bundler {
	return &esbuildBundler{opts: opts, outdir: filepath.Join(opts.Root, "_orbit")}
}

// fileLoaders are the extensions that CSS (url(…)) and scripts may import
// as files: copied to /_orbit/ with a hashed name, and referenced by URL.
var fileLoaders = []string{".avif", ".bmp", ".eot", ".gif", ".ico", ".jpeg", ".jpg", ".otf", ".png", ".svg", ".ttf", ".webp", ".woff", ".woff2"}

func (b *esbuildBundler) buildOptions(entries []string) api.BuildOptions {
	loaders := map[string]api.Loader{}
	for _, ext := range fileLoaders {
		loaders[ext] = api.LoaderFile
	}
	eps := make([]api.EntryPoint, len(entries))
	for i, spec := range entries {
		// "orbit-page:blog-slug.ts" → [name] blog-slug
		name := strings.TrimPrefix(spec, pageNamespace+":")
		eps[i] = api.EntryPoint{InputPath: spec, OutputPath: name[:strings.LastIndexByte(name, '.')]}
	}
	o := api.BuildOptions{
		AbsWorkingDir:       b.opts.Root,
		EntryPointsAdvanced: eps,
		Bundle:              true,
		Format:              api.FormatESModule,
		Platform:            api.PlatformBrowser,
		Outdir:              b.outdir,
		EntryNames:          "[name]-[hash]",
		AssetNames:          "[name]-[hash]",
		PublicPath:          strings.TrimSuffix(URLPrefix, "/"),
		Loader:              loaders,
		Write:               false,
		Metafile:            true,
		LogLevel:            api.LogLevelSilent,
		Plugins:             []api.Plugin{b.plugin()},
	}
	if b.opts.Dev {
		o.Sourcemap = api.SourceMapLinked
	} else {
		o.MinifyWhitespace, o.MinifyIdentifiers, o.MinifySyntax = true, true, true
		if b.opts.Sourcemap {
			o.Sourcemap = api.SourceMapExternal
		}
	}
	return o
}

func (b *esbuildBundler) current() *plan {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.plan
}

// plugin serves the virtual modules of the current plan.
func (b *esbuildBundler) plugin() api.Plugin {
	return api.Plugin{
		Name: "orbit",
		Setup: func(pb api.PluginBuild) {
			pb.OnResolve(api.OnResolveOptions{Filter: "^" + pageNamespace + ":"}, func(a api.OnResolveArgs) (api.OnResolveResult, error) {
				return api.OnResolveResult{Path: strings.TrimPrefix(a.Path, pageNamespace+":"), Namespace: pageNamespace}, nil
			})
			pb.OnResolve(api.OnResolveOptions{Filter: "^orbit:(script|style)/"}, func(a api.OnResolveArgs) (api.OnResolveResult, error) {
				blk := b.current().blocks[a.Path]
				if blk == nil {
					return api.OnResolveResult{}, fmt.Errorf("unknown Orbit module %s", a.Path)
				}
				return api.OnResolveResult{
					Path:      filepath.Join(b.opts.Root, filepath.FromSlash(blk.file)),
					Namespace: "file",
					Suffix:    blk.suffix,
				}, nil
			})
			pb.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: pageNamespace}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
				src, ok := b.current().pageSrc[a.Path]
				if !ok {
					return api.OnLoadResult{}, fmt.Errorf("unknown page entry %s", a.Path)
				}
				loader := api.LoaderTS
				if strings.HasSuffix(a.Path, ".css") {
					loader = api.LoaderCSS
				}
				return api.OnLoadResult{Contents: &src, Loader: loader, ResolveDir: b.opts.Root}, nil
			})
			pb.OnLoad(api.OnLoadOptions{Filter: `\.orbit$`, Namespace: "file"}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
				rel, err := filepath.Rel(b.opts.Root, a.Path)
				if err != nil {
					return api.OnLoadResult{}, err
				}
				blk := b.current().byKey[filepath.ToSlash(rel)+a.Suffix]
				if blk == nil {
					return api.OnLoadResult{}, errors.New("a script can't import a .orbit file; import a .ts file instead")
				}
				loader := api.LoaderTS
				if blk.css {
					loader = api.LoaderCSS
				}
				return api.OnLoadResult{Contents: &blk.contents, Loader: loader, ResolveDir: filepath.Dir(a.Path)}, nil
			})
		},
	}
}

// Bundle builds every page of in.
func (b *esbuildBundler) Bundle(in *Input) (*Result, error) {
	p := newPlan(in)
	res := &Result{Pages: map[string][]Asset{}, Diagnostics: p.diagnostics}
	if res.HasErrors() {
		return res, nil
	}
	b.mu.Lock()
	b.plan = p
	b.mu.Unlock()
	if len(p.entries) == 0 {
		b.dispose()
		return res, nil
	}
	if b.ctx == nil || b.ctxKey != p.key() {
		b.dispose()
		ctx, cerr := api.Context(b.buildOptions(p.entries))
		if cerr != nil {
			return nil, fmt.Errorf("esbuild: %s", messagesText(cerr.Errors))
		}
		b.ctx, b.ctxKey = ctx, p.key()
		b.contexts++
	}
	r := b.ctx.Rebuild()
	for _, m := range r.Errors {
		res.Diagnostics = append(res.Diagnostics, b.diagnostic(m, false))
	}
	for _, m := range r.Warnings {
		res.Diagnostics = append(res.Diagnostics, b.diagnostic(m, true))
	}
	if len(r.Errors) > 0 {
		return res, nil
	}
	if err := b.collect(p, r, res); err != nil {
		return nil, err
	}
	return res, nil
}

func (b *esbuildBundler) dispose() {
	if b.ctx != nil {
		b.ctx.Dispose()
		b.ctx, b.ctxKey = nil, ""
	}
}

// Close disposes of the esbuild context.
func (b *esbuildBundler) Close() { b.dispose() }

// metafile is the part of esbuild's metafile that Bundle reads.
type metafile struct {
	Outputs map[string]struct {
		EntryPoint string `json:"entryPoint"`
		CSSBundle  string `json:"cssBundle"`
	} `json:"outputs"`
}

// collect turns esbuild's output files into Result files and page assets.
func (b *esbuildBundler) collect(p *plan, r api.BuildResult, res *Result) error {
	var meta metafile
	if err := json.Unmarshal([]byte(r.Metafile), &meta); err != nil {
		return fmt.Errorf("esbuild metafile: %w", err)
	}
	files := map[string][]byte{} // by name under /_orbit/
	for _, of := range r.OutputFiles {
		rel, err := filepath.Rel(b.outdir, of.Path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("esbuild output %s is outside the output directory", of.Path)
		}
		files[filepath.ToSlash(rel)] = of.Contents
	}
	// The metafile's output keys are relative to the working directory:
	// "_orbit/index-HASH.js".
	name := func(key string) string { return strings.TrimPrefix(filepath.ToSlash(key), "_orbit/") }
	var empty []string
	for key, out := range meta.Outputs {
		page, ok := p.pageOf[out.EntryPoint]
		if !ok {
			continue
		}
		n := name(key)
		switch {
		case strings.HasSuffix(n, ".css"):
			res.Pages[page] = append(res.Pages[page], Asset{Kind: "css", URL: URLPrefix + n})
		case strings.HasSuffix(n, ".js"):
			if out.CSSBundle != "" {
				res.Pages[page] = append(res.Pages[page], Asset{Kind: "css", URL: URLPrefix + name(out.CSSBundle)})
			}
			if isEmptyJS(files[n]) {
				empty = append(empty, n)
				continue
			}
			res.Pages[page] = append(res.Pages[page], Asset{Kind: "js", URL: URLPrefix + n})
		}
	}
	for _, n := range empty {
		delete(files, n)
		delete(files, n+".map")
	}
	for page, as := range res.Pages {
		// The stylesheet first, then the module.
		slices.SortStableFunc(as, func(x, y Asset) int { return strings.Compare(x.Kind, y.Kind) })
		res.Pages[page] = as
	}
	for n, data := range files {
		if strings.HasSuffix(n, ".map") {
			fixed, err := b.fixSourceMap(n, data, p)
			if err != nil {
				return err
			}
			data = fixed
		}
		res.Files = append(res.Files, File{Name: n, Contents: data})
	}
	slices.SortFunc(res.Files, func(x, y File) int { return strings.Compare(x.Name, y.Name) })
	return nil
}

var sourceMappingURL = regexp.MustCompile(`(?m)^//# sourceMappingURL=\S*\s*$`)

// isEmptyJS reports whether a JS bundle has no code (a page whose scripts
// only declare types): it gets no <script> tag.
func isEmptyJS(js []byte) bool {
	return len(strings.TrimSpace(sourceMappingURL.ReplaceAllString(string(js), ""))) == 0
}

// diagnostic converts an esbuild message.
func (b *esbuildBundler) diagnostic(m api.Message, warning bool) Diagnostic {
	d := Diagnostic{Warning: warning, Message: m.Text}
	if loc := m.Location; loc != nil {
		file := loc.File
		if i := strings.Index(file, "?orbit-"); i >= 0 {
			file = file[:i]
		}
		if filepath.IsAbs(file) {
			if rel, err := filepath.Rel(b.opts.Root, file); err == nil {
				file = rel
			}
		}
		d.File = filepath.ToSlash(file)
		d.Line, d.Col = loc.Line, loc.Column+1
	}
	return d
}

func messagesText(ms []api.Message) string {
	var s []string
	for _, m := range ms {
		s = append(s, m.Text)
	}
	return strings.Join(s, "; ")
}
