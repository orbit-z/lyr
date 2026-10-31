package assets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/orbit-z/lyr/internal/tailwind"
)

// esbuildBundler is the Bundler built on esbuild's Go API. It keeps one
// incremental build context, reused by Bundle while the set of entries
// stays the same; the plugin reads the current plan, so a changed <style>
// or <script> only needs a Rebuild.
type esbuildBundler struct {
	opts   Options
	outdir string // virtual output directory: <root>/_lyr (nothing is written)

	mu   sync.RWMutex
	plan *plan // read by the plugin during a build

	ctx      api.BuildContext
	ctxKey   string
	contexts int // contexts created, for tests

	// For tests: noSplit turns code splitting off (to compare the
	// stylesheets with and without it), twLoads counts the loads of
	// Tailwind blocks.
	noSplit bool
	twLoads atomic.Int32

	twOnce sync.Once
	tw     *tailwind.Processor // Options.Tailwind, or made on first use
}

// New returns the esbuild Bundler.
func New(opts Options) Bundler {
	return &esbuildBundler{opts: opts, outdir: filepath.Join(opts.Root, "_lyr"), tw: opts.Tailwind}
}

// fileLoaders are the extensions that CSS (url(…)) and scripts may import
// as files: copied to /_lyr/ with a hashed name, and referenced by URL.
var fileLoaders = []string{".avif", ".bmp", ".eot", ".gif", ".ico", ".jpeg", ".jpg", ".otf", ".png", ".svg", ".ttf", ".webp", ".woff", ".woff2"}

func (b *esbuildBundler) buildOptions(p *plan) api.BuildOptions {
	loaders := map[string]api.Loader{}
	for _, ext := range fileLoaders {
		loaders[ext] = api.LoaderFile
	}
	eps := make([]api.EntryPoint, len(p.entries))
	for i, spec := range p.entries {
		// "lyr-page:blog-slug.ts" → [name] blog-slug,
		// "lyr:lazy/Counter-1c2d3e4f.ts" → Counter
		eps[i] = api.EntryPoint{InputPath: spec, OutputPath: p.outName[spec]}
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
		// Code splitting (PLAN.md §5e): the modules that several entries
		// import, and dynamic import() targets, are chunks, flat in
		// /_lyr/ next to the entries.
		Splitting:  !b.noSplit,
		ChunkNames: "[name]-[hash]",
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
		Name: "lyr",
		Setup: func(pb api.PluginBuild) {
			pb.OnResolve(api.OnResolveOptions{Filter: "^" + pageNamespace + ":"}, func(a api.OnResolveArgs) (api.OnResolveResult, error) {
				return api.OnResolveResult{Path: strings.TrimPrefix(a.Path, pageNamespace+":"), Namespace: pageNamespace}, nil
			})
			pb.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: highlightNamespace}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
				css := b.current().highlight
				return api.OnLoadResult{Contents: &css, Loader: api.LoaderCSS, ResolveDir: b.opts.Root}, nil
			})
			pb.OnResolve(api.OnResolveOptions{Filter: "^lyr:(script|style)/"}, func(a api.OnResolveArgs) (api.OnResolveResult, error) {
				if a.Path == highlightSpec {
					return api.OnResolveResult{Path: strings.TrimPrefix(a.Path, stylePrefix), Namespace: highlightNamespace}, nil
				}
				blk := b.current().blocks[a.Path]
				if blk.block == nil {
					return api.OnResolveResult{}, fmt.Errorf("unknown Lyr module %s", a.Path)
				}
				return api.OnResolveResult{
					Path:      filepath.Join(b.opts.Root, filepath.FromSlash(blk.file)),
					Namespace: "file",
					Suffix:    blk.suffix,
				}, nil
			})
			pb.OnResolve(api.OnResolveOptions{Filter: `^lyr:(client|types)$`}, func(a api.OnResolveArgs) (api.OnResolveResult, error) {
				return api.OnResolveResult{Path: a.Path, Namespace: clientNamespace}, nil
			})
			pb.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: clientNamespace}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
				src := ""
				if a.Path == "lyr:client" {
					src = clientTS
				}
				// lyr:types only has types: empty at runtime.
				return api.OnLoadResult{Contents: &src, Loader: api.LoaderTS, ResolveDir: b.opts.Root}, nil
			})
			pb.OnResolve(api.OnResolveOptions{Filter: "^" + lazyPrefix}, func(a api.OnResolveArgs) (api.OnResolveResult, error) {
				return api.OnResolveResult{Path: strings.TrimPrefix(a.Path, lazyPrefix), Namespace: lazyNamespace}, nil
			})
			pb.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: lazyNamespace}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
				src, ok := b.current().lazySrc[a.Path]
				if !ok {
					return api.OnLoadResult{}, fmt.Errorf("unknown lazy entry %s", a.Path)
				}
				return api.OnLoadResult{Contents: &src, Loader: api.LoaderTS, ResolveDir: b.opts.Root}, nil
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
			pb.OnLoad(api.OnLoadOptions{Filter: `\.lyr$`, Namespace: "file"}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
				// Called once per block copy: a prefix cut, not filepath.Rel
				// (its Clean calls showed in profiles).
				rel, ok := strings.CutPrefix(a.Path, b.opts.Root+string(filepath.Separator))
				if !ok {
					var err error
					if rel, err = filepath.Rel(b.opts.Root, a.Path); err != nil {
						return api.OnLoadResult{}, err
					}
				}
				blk := b.current().byKey[filepath.ToSlash(rel)+a.Suffix]
				if blk == nil {
					// An import of a component from a script: its client
					// module (PLAN.md §5c), the same for every component.
					src := componentModule
					return api.OnLoadResult{Contents: &src, Loader: api.LoaderJS, ResolveDir: b.opts.Root}, nil
				}
				if blk.tw.Input {
					// Once for all the block's per-page copies.
					blk.twOnce.Do(func() {
						b.twLoads.Add(1)
						blk.twRes = b.loadTailwindBlock(pb, blk, a.Path)
					})
					return blk.twRes, nil
				}
				loader := api.LoaderTS
				if blk.css {
					loader = api.LoaderCSS
				}
				return api.OnLoadResult{Contents: &blk.contents, Loader: loader, ResolveDir: filepath.Dir(a.Path)}, nil
			})
			pb.OnLoad(api.OnLoadOptions{Filter: `\.css$`, Namespace: "file"}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
				return b.loadCSS(pb, a)
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
		ctx, cerr := api.Context(b.buildOptions(p))
		if cerr != nil {
			return nil, fmt.Errorf("esbuild: %s", messagesText(cerr.Errors))
		}
		b.ctx, b.ctxKey = ctx, p.key()
		b.contexts++
	}
	tw := b.tw
	if tw != nil {
		tw.Begin()
	}
	r := b.ctx.Rebuild()
	if tw == nil {
		tw = b.tw // made during this build
	}
	if tw != nil && len(r.Errors) == 0 {
		tw.Sweep()
	}
	// A block copied into several pages is reported once per copy by
	// esbuild: deduplicated by position and message.
	seen := map[Diagnostic]bool{}
	add := func(d Diagnostic) {
		if !seen[d] {
			seen[d] = true
			res.Diagnostics = append(res.Diagnostics, d)
		}
	}
	for _, m := range r.Errors {
		add(b.diagnostic(m, false))
	}
	for _, m := range r.Warnings {
		add(b.diagnostic(m, true))
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
	Outputs map[string]metaOutput `json:"outputs"`
}

type metaOutput struct {
	EntryPoint string `json:"entryPoint"`
	CSSBundle  string `json:"cssBundle"`
	Imports    []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"imports"`
}

// collect turns esbuild's output files into Result files and page assets.
func (b *esbuildBundler) collect(p *plan, r api.BuildResult, res *Result) error {
	var meta metafile
	if err := json.Unmarshal([]byte(r.Metafile), &meta); err != nil {
		return fmt.Errorf("esbuild metafile: %w", err)
	}
	files := map[string][]byte{} // by name under /_lyr/
	for _, of := range r.OutputFiles {
		rel, err := filepath.Rel(b.outdir, of.Path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("esbuild output %s is outside the output directory", of.Path)
		}
		files[filepath.ToSlash(rel)] = of.Contents
	}
	// The metafile's output keys, and the paths of their imports, are
	// relative to the working directory: "_lyr/index-HASH.js".
	name := func(key string) string { return strings.TrimPrefix(filepath.ToSlash(key), "_lyr/") }
	outs := make(map[string]metaOutput, len(meta.Outputs)) // by name
	for key, out := range meta.Outputs {
		outs[name(key)] = out
	}
	b.dropEmptyChunks(p, outs, files)

	// preloads returns the chunks in the static import closure of the JS
	// output n, in import order (PLAN.md §5e rule 9).
	preloads := func(n string) []Asset {
		var as []Asset
		seen := map[string]bool{n: true}
		queue := []string{n}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, imp := range outs[cur].Imports {
				c := name(imp.Path)
				if imp.Kind != "import-statement" || seen[c] || files[c] == nil {
					continue
				}
				seen[c] = true
				as = append(as, Asset{Kind: "modulepreload", URL: URLPrefix + c})
				queue = append(queue, c)
			}
		}
		return as
	}

	lazyJS, lazyCSS := map[string]string{}, map[string]string{} // by component file
	for n, out := range outs {
		if file, ok := p.lazyOf[out.EntryPoint]; ok {
			if !strings.HasSuffix(n, ".js") {
				continue
			}
			if out.CSSBundle != "" {
				lazyCSS[file] = URLPrefix + name(out.CSSBundle)
			}
			if !isEmptyJS(files[n]) {
				lazyJS[file] = URLPrefix + n
			}
			continue
		}
		page, ok := p.pageOf[out.EntryPoint]
		if !ok {
			continue
		}
		switch {
		case strings.HasSuffix(n, ".css"):
			res.Pages[page] = append(res.Pages[page], Asset{Kind: "css", URL: URLPrefix + n})
		case strings.HasSuffix(n, ".js"):
			if out.CSSBundle != "" {
				res.Pages[page] = append(res.Pages[page], Asset{Kind: "css", URL: URLPrefix + name(out.CSSBundle)})
			}
			if isEmptyJS(files[n]) {
				continue
			}
			res.Pages[page] = append(res.Pages[page], Asset{Kind: "js", URL: URLPrefix + n})
			res.Pages[page] = append(res.Pages[page], preloads(n)...)
		}
	}
	// Each page's lazy assets, in Lazy order (sorted by ID), and the CSS
	// of its lazy entries after its own stylesheet.
	for _, pg := range p.pages {
		cssSeen := map[string]bool{}
		for _, l := range pg.Lazy {
			for _, file := range l.Modules {
				if css := lazyCSS[file]; css != "" && !cssSeen[css] {
					cssSeen[css] = true
					res.Pages[pg.File] = append(res.Pages[pg.File], Asset{Kind: "css", URL: css})
				}
				if js := lazyJS[file]; js != "" {
					res.Pages[pg.File] = append(res.Pages[pg.File], Asset{Kind: "lazy", URL: js, ID: l.ID})
				}
			}
		}
	}
	linked := map[string]bool{}
	for page, as := range res.Pages {
		// The stylesheets first, then the module, then the lazy entries,
		// then the module's chunks.
		slices.SortStableFunc(as, func(x, y Asset) int { return strings.Compare(x.Kind, y.Kind) })
		res.Pages[page] = as
		for _, a := range as {
			linked[strings.TrimPrefix(a.URL, URLPrefix)] = true
		}
	}
	for n, out := range outs {
		// Empty JS entries (a page or lazy entry whose scripts only
		// declare types) and the stylesheets that nothing links: those
		// of dynamic import() chunks, whose CSS is in the stylesheet of
		// every page that can reach them (PLAN.md §5e rule 7).
		_, entry := p.pageOf[out.EntryPoint]
		_, lazy := p.lazyOf[out.EntryPoint]
		if (strings.HasSuffix(n, ".js") && (entry || lazy) && !linked[n]) ||
			(strings.HasSuffix(n, ".css") && !linked[n]) {
			delete(files, n)
			delete(files, n+".map")
		}
	}
	for n, data := range files {
		res.Files = append(res.Files, File{Name: n, Contents: data})
	}
	slices.SortFunc(res.Files, func(x, y File) int { return strings.Compare(x.Name, y.Name) })
	// The source maps are rewritten in parallel: decoding and encoding
	// them is most of collect's time in a dev rebuild.
	var wg sync.WaitGroup
	errs := make([]error, len(res.Files))
	for i := range res.Files {
		f := &res.Files[i]
		if !strings.HasSuffix(f.Name, ".map") {
			continue
		}
		wg.Go(func() {
			f.Contents, errs[i] = b.fixSourceMap(f.Name, f.Contents, p)
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

// dropEmptyChunks removes the shared chunks with no code (PLAN.md §5e rule
// 8): esbuild makes one for a CSS file or a types-only module imported
// from scripts of several entries. The side-effect imports of a removed
// chunk are deleted from every JS file, or replaced by as many spaces
// where a source map exists so that its mappings stay valid, until
// nothing changes (a chunk left with only such imports is empty too).
// Entries and dynamic import() targets are kept: an import() of a removed
// file would fail.
func (b *esbuildBundler) dropEmptyChunks(p *plan, outs map[string]metaOutput, files map[string][]byte) {
	dynamic := map[string]bool{}
	for _, out := range outs {
		for _, imp := range out.Imports {
			if imp.Kind == "dynamic-import" {
				dynamic[strings.TrimPrefix(filepath.ToSlash(imp.Path), "_lyr/")] = true
			}
		}
	}
	blank := b.opts.Dev || b.opts.Sourcemap
	for changed := true; changed; {
		changed = false
		for n, out := range outs {
			_, entry := p.pageOf[out.EntryPoint]
			_, lazy := p.lazyOf[out.EntryPoint]
			if entry || lazy || dynamic[n] || !strings.HasSuffix(n, ".js") || files[n] == nil || !isEmptyJS(files[n]) {
				continue
			}
			changed = true
			delete(files, n)
			delete(files, n+".map")
			url := strconv.Quote(URLPrefix + n)
			for m, data := range files {
				if !strings.HasSuffix(m, ".js") {
					continue
				}
				for _, stmt := range []string{"import" + url + ";", "import " + url + ";\n", "import " + url + ";"} {
					with := ""
					if blank {
						with = strings.Repeat(" ", len(stmt))
						if strings.HasSuffix(stmt, "\n") {
							with = with[1:] + "\n"
						}
					}
					data = bytes.ReplaceAll(data, []byte(stmt), []byte(with))
				}
				files[m] = data
			}
		}
	}
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
		if i := strings.Index(file, "?lyr-"); i >= 0 {
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
