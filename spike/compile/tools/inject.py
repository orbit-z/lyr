#!/usr/bin/env python3
"""Error-injection harness for the compile spike.

Copies the module to $SCRATCH/<case>, applies (file, old, new) edits to the
.orbit source and to the hand-written generated span, re-fixes the reset
directives, then runs go build / go vet from several working directories.
Usage: tools/inject.py SCRATCH CASE...   (CASE names from CASES, or 'all')
"""
import os, shutil, subprocess, sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
A_ = 'src/components/A.orbit'; B_ = 'src/components/B.orbit'; I_ = 'src/pages/index.orbit'
AI = 'orbit_gen/impl/components/a/render.go'; BI = 'orbit_gen/impl/components/b/render.go'
II = 'orbit_gen/impl/pages/index/render.go'; AD = 'orbit_gen/decl/components/a/decl.go'

CASES = {
  # (a) type error in a frontmatter statement: expect A.orbit:21:16
  'a_frontmatter': [(A_, 'title := label(props.Depth)', 'title := label("x")'),
                    (AI, 'title := label(props.Depth)', 'title := label("x")')],
  # (b) undefined name in a template expression: expect A.orbit:25:8
  'b_expr': [(A_, '<h2>{title}', '<h2>{titel}'),
             (AI, 'A.orbit:25:8*/title/', 'A.orbit:25:8*/titel/')],
  # (c) unknown prop field at a call site: expect index.orbit:9:6
  'c_unknown_prop': [(I_, '<A item={it}', '<A psot={it}'),
                     (II, 'index.orbit:9:6*/Item/', 'index.orbit:9:6*/Psot/')],
  # (d) type error inside a hoisted lowercase helper: expect B.orbit:16:55
  'd_helper': [(B_, 'strconv.Itoa(n)', 'strconv.Itoa("n")'),
               (BI, 'strconv.Itoa(n)', 'strconv.Itoa("n")')],
  # (d2) error on a continuation line of a multi-line decl span: expect A.orbit:9:11
  'd2_decl_multiline': [(A_, '    Count int\n', '    Count intt\n'),
                        (AD, '    Count int\n', '    Count intt\n')],
  # (e) error in an {if} control header: expect A.orbit:29:13
  'e_if': [(A_, '{if props.Depth < 4}', '{if props.Dpth < 4}'),
           (AI, '*/props.Depth < 4/', '*/props.Dpth < 4/')],
  # (e2) non-bool {if} header: expect A.orbit:26:7
  'e2_if_nonbool': [(A_, '{if props.Item.Count > 0}', '{if props.Item.Count}'),
                    (AI, '*/props.Item.Count > 0/', '*/props.Item.Count/')],
  # (3) glue error right after a span, reset present: expect render.go:29:<col after title>
  'r_glue_after_span': [(AI, 'A.orbit:25:8*/title/*line render.go:29:78*/,',
                              'A.orbit:25:8*/title/*line render.go:29:78*/ + orbit__glue,')],
  # (3') same, but the reset directive removed: shows why resets are needed
  'r_glue_no_reset': [(AI, 'A.orbit:25:8*/title/*line render.go:29:78*/,',
                            'A.orbit:25:8*/title + orbit__glue,')],
  # (4) impl references an unexported decl name
  'u_unexported': [(AD, '/*line ../../../../src/components/A.orbit:14:1*/type Props',
                        'func shout(s string) string { return s + "!" }\n\n/*line ../../../../src/components/A.orbit:14:1*/type Props'),
                   (AI, 'title := label(props.Depth)', 'title := shout(label(props.Depth))')],
  # (6) dot-import clash: impl declares a name that decl exports
  'x_dot_clash_decl': [(AI, 'func init() {', 'type Item struct{}\n\nfunc init() {')],
  # (6') dot-import clash: an import alias (e.g. a component named Item) equal to a decl export
  'x_dot_clash_import': [(AI, '\tB /*line', '\tItem "example.com/blog/orbit_gen/decl/components/b"\n\tB /*line'),
                         (AI, 'B.Tag{Text: title}', 'Item.Tag{Text: title}')],
  # (6'') user body declares a local named like a decl export: shadowing is legal
  'x_dot_shadow_local': [(AI, 'unused := 42', 'Item := 42')],
  # (6'''') a body local named `orbit` shadows the runtime package used by glue
  'x_orbit_shadow': [(A_, 'unused := 42', 'orbit := 42'),
                     (AI, 'unused := 42', 'orbit := 42'), (AI, '\t_ = unused\n', '\t_ = orbit\n')],
  # (6v) frontmatter ending in an unconditional return (redirect-only page): vet unreachable?
  'x_unreachable': [(A_, 'unused := 42\n', 'unused := 42\nreturn nil\n'),
                    (AI, '/*line ../../../../src/components/A.orbit:22:1*/unused := 42/*line render.go:24:85*/\n',
                         '/*line ../../../../src/components/A.orbit:22:1*/unused := 42\nreturn nil/*line render.go:24:85*/\n')],
  # (6vi) hoisted user func init() rendering another component / itself
  'x_init_cross': [(AI, 'func init() { Orbit__Render = orbit__render }',
                        'func init() { Orbit__Render = orbit__render }\n\nfunc init() { _ = B.Render(&orbit.Ctx{}, nil, B.Props{}, orbit.Slots{}) }')],
  'x_init_self_before': [(AI, 'func init() { Orbit__Render = orbit__render }',
                        'func init() { _ = Render(&orbit.Ctx{}, orbit.NewWriter(new(bytes.Buffer)), Props{}, orbit.Slots{}) }\n\nfunc init() { Orbit__Render = orbit__render }'),
                         (AI, 'import (\n', 'import (\n\t"bytes"\n')],
  # fix candidate: glue uses a second import name orbit__rt, so a user `orbit` local is harmless
  'f_orbit_rt_alias': [(A_, 'unused := 42', 'orbit := 42'),
                       (AI, 'unused := 42', 'orbit := 42'), (AI, '\t_ = unused\n', '\t_ = orbit\n'),
                       (AI, '\torbit "example.com/blog/orbitrt"\n', '\torbit__rt "example.com/blog/orbitrt"\n'),
                       (AI, 'orbit.Slots{}); orbit__err != nil {\n\t\t\treturn orbit__err\n\t\t}\n\t\torbit__w.Raw("\\n  ")\n\t}\n\torbit__w.Raw("\\n  ")',
                            'orbit__rt.Slots{}); orbit__err != nil {\n\t\t\treturn orbit__err\n\t\t}\n\t\torbit__w.Raw("\\n  ")\n\t}\n\torbit__w.Raw("\\n  ")'),
                       (AI, '}, orbit.Slots{}); orbit__err != nil {\n\t\t\treturn orbit__err\n\t\t}\n\t\torbit__w.Raw("\\n  ")\n\t}\n\torbit__w.Raw("\\n</div>',
                            '}, orbit__rt.Slots{}); orbit__err != nil {\n\t\t\treturn orbit__err\n\t\t}\n\t\torbit__w.Raw("\\n  ")\n\t}\n\torbit__w.Raw("\\n</div>'),
                       (AI, 'func orbit__render(ctx *orbit.Ctx, orbit__w *orbit.Writer, props Props, slots orbit.Slots) error',
                            'func orbit__render(ctx *orbit__rt.Ctx, orbit__w *orbit__rt.Writer, props Props, slots orbit__rt.Slots) error'),
                       (AI, 'orbit.LocalsOf[orbit.NoLocals](ctx)', 'orbit__rt.LocalsOf[orbit__rt.NoLocals](ctx)')],
  # nested shadowing of ctx (allowed by §2.2) breaks glue that passes `ctx` to child Render calls
  'x_ctx_shadow_nested': [(AI, '\tif /*line ../../../../src/components/A.orbit:29:7*/',
                               '\tfor _, ctx := range []int{1} {\n\t_ = ctx\n\tif /*line ../../../../src/components/A.orbit:29:7*/'),
                          (AI, '\torbit__w.Raw("\\n</div>\\n")', '\t}\n\torbit__w.Raw("\\n</div>\\n")')],
  # fix candidate: link in a package-level var initializer (runs before every init() of the package)
  'f_init_var_link': [(AI, 'func init() { Orbit__Render = orbit__render }',
                        'func init() { _ = Render(&orbit.Ctx{}, orbit.NewWriter(new(bytes.Buffer)), Props{Depth: 9}, orbit.Slots{}); println("user init rendered self OK") }\n\nvar _ = func() int { Orbit__Render = orbit__render; return 0 }()'),
                      (AI, 'import (\n', 'import (\n\t"bytes"\n')],
  # runtime panic inside a template expression: where do stack traces point?
  'p_runtime_panic': [(A_, '{props.Item.Title()}', '{strings.Repeat(props.Item.Name, props.Depth-1)}'),
                      (AI, '*/props.Item.Title()/', '*/strings.Repeat(props.Item.Name, props.Depth-1)/')],
  # (6''') missing `_ = x`: what Go reports and where
  'x_unused_missing': [(AI, '\t_ = unused\n', '')],
}

def run(cmd, cwd):
    p = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True)
    return p.returncode, (p.stdout + p.stderr).strip()

def main():
    scratch, names = sys.argv[1], sys.argv[2:]
    if names == ['all']:
        names = list(CASES)
    for name in names:
        dst = os.path.join(scratch, name)
        shutil.rmtree(dst, ignore_errors=True)
        shutil.copytree(ROOT, dst, ignore=shutil.ignore_patterns('FINDINGS.md'))
        for f, old, new in CASES[name]:
            p = os.path.join(dst, f)
            s = open(p).read()
            assert s.count(old) == 1, (name, f, old, s.count(old))
            open(p, 'w').write(s.replace(old, new))
        gen = [os.path.join(r, x) for r, _, fs in os.walk(os.path.join(dst, 'orbit_gen')) for x in fs if x.endswith('.go')]
        if name != 'r_glue_no_reset':
            subprocess.run([sys.executable, os.path.join(ROOT, 'tools/lines.py'), 'fix'] + gen, check=True)
        print('=' * 20, name)
        for label, cmd, cwd in [
            ('build @root', ['go', 'build', './...'], dst),
            ('vet   @root', ['go', 'vet', './...'], dst),
            ('build @src/', ['go', 'build', '../...'], os.path.join(dst, 'src')),
            ('vet   @src/', ['go', 'vet', '../...'], os.path.join(dst, 'src')),
            ('build @pkgdir', ['go', 'build', '.'], os.path.join(dst, os.path.dirname(AI))),
            ('run   @root', ['go', 'run', '.'], dst),
        ]:
            rc, out = run(cmd, cwd)
            print('--- %s $ %s  (exit %d)' % (label, ' '.join(cmd), rc))
            print(out)

main()
