package assets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/tailwind"
)

// Tailwind CSS (PLAN.md §5d). A .css file esbuild loads, or a <style>
// block, that is a Tailwind input goes through Tailwind's CLI in esbuild's
// load step, before esbuild parses it; esbuild then bundles, minifies,
// hashes and resolves url() in the output as for any CSS. esbuild loads
// files in parallel, so the inputs run in parallel. Every other CSS file
// is left to esbuild's own loader, after a quick text check.

// tailwind returns the Tailwind processor: Options.Tailwind, or one made
// on first use.
func (b *esbuildBundler) tailwind() *tailwind.Processor {
	b.twOnce.Do(func() {
		if b.tw == nil {
			b.tw = tailwind.NewProcessor(b.opts.Root)
		}
	})
	return b.tw
}

// loadCSS is the file-namespace load step of .css files: nil contents
// (esbuild's own loader) unless the file is a Tailwind input.
func (b *esbuildBundler) loadCSS(pb api.PluginBuild, a api.OnLoadArgs) (api.OnLoadResult, error) {
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return api.OnLoadResult{}, nil // esbuild reports it
	}
	text := string(data)
	if !tailwind.Detect(text).Input {
		return api.OnLoadResult{}, nil
	}
	dir := filepath.Dir(a.Path)
	out, err := b.runTailwind(pb, tailwind.Source{Text: text, File: a.Path, Line: 1, Col: 1, Dir: dir})
	if err != nil {
		return api.OnLoadResult{Errors: []api.Message{tailwindMessage(err)}}, nil
	}
	return api.OnLoadResult{Contents: &out, Loader: api.LoaderCSS, ResolveDir: dir}, nil
}

// loadTailwindBlock returns the contents of a <style> block that is a
// Tailwind input: Tailwind's output, after the block's padding.
func (b *esbuildBundler) loadTailwindBlock(pb api.PluginBuild, blk *block, path string) (api.OnLoadResult, error) {
	dir := filepath.Dir(path)
	out, err := b.runTailwind(pb, tailwind.Source{Text: blk.text, File: path, Line: blk.start.Line, Col: blk.start.Col, Dir: dir})
	if err != nil {
		var te *tailwind.Error
		if errors.As(err, &te) && te.File == path && blk.scope != "" {
			blockPosition(te, blk)
		}
		return api.OnLoadResult{Errors: []api.Message{tailwindMessage(err)}}, nil
	}
	contents := pad(compiler.Position{Line: blk.start.Line, Col: 1}) + out
	return api.OnLoadResult{Contents: &contents, Loader: api.LoaderCSS, ResolveDir: dir}, nil
}

// runTailwind inlines src's CSS imports (with esbuild's resolver) and runs
// Tailwind over the result.
func (b *esbuildBundler) runTailwind(pb api.PluginBuild, src tailwind.Source) (string, error) {
	resolve := func(spec, dir string) (string, bool) {
		r := pb.Resolve(spec, api.ResolveOptions{Kind: api.ResolveCSSImportRule, ResolveDir: dir})
		if len(r.Errors) > 0 || r.External || r.Namespace != "file" || r.Path == "" || r.Suffix != "" {
			return "", false
		}
		return r.Path, true
	}
	read := func(p string) (string, error) {
		data, err := os.ReadFile(p)
		return string(data), err
	}
	inl := tailwind.Inline(src, tailwind.InlineOptions{Resolve: resolve, Read: read, Root: b.opts.Root})
	info := tailwind.Detect(inl.Text)
	return b.tailwind().Process(context.Background(), tailwind.Input{
		Inlined: inl, Dir: src.Dir, Root: info.Root,
		Marker: info.Pos, MarkerName: tailwind.MarkerName(inl.Text, info.Pos),
	})
}

func tailwindMessage(err error) api.Message {
	m := api.Message{Text: err.Error()}
	var te *tailwind.Error
	if errors.As(err, &te) {
		m.Text = te.Msg
		if te.Line > 0 {
			m.Location = &api.Location{File: te.File, Line: te.Line, Column: te.Col - 1}
		}
	}
	return m
}

// blockPosition moves an error in a scoped block from the scoped text,
// which Tailwind saw, to the text as written: the scoper inserts the
// scope attribute and removes :global( and its ")" without adding or
// removing lines.
func blockPosition(te *tailwind.Error, blk *block) {
	line := te.Line - blk.start.Line
	if line < 0 {
		return
	}
	scoped, orig := nthLine(blk.text, line), nthLine(blk.orig, line)
	col := te.Col - 1
	if line == 0 {
		col -= blk.start.Col - 1
	}
	if col < 0 || col > len(scoped) {
		return
	}
	i, j := 0, 0
	for j < col && i < len(orig) {
		switch {
		case strings.HasPrefix(scoped[j:], blk.scope) && !strings.HasPrefix(orig[i:], blk.scope):
			j += len(blk.scope)
		case orig[i] == scoped[j]:
			i++
			j++
		default:
			i++
		}
	}
	if line == 0 {
		i += blk.start.Col - 1
	}
	te.Col = i + 1
}

// nthLine returns line n (0-based) of s.
func nthLine(s string, n int) string {
	for ; n > 0; n-- {
		k := strings.IndexByte(s, '\n')
		if k < 0 {
			return ""
		}
		s = s[k+1:]
	}
	l, _, _ := strings.Cut(s, "\n")
	return l
}
