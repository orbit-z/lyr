#!/usr/bin/env python3
"""Spike helper (stands in for codegen's position tracking).

fix:   rewrites every reset directive /*line <own-basename>:L:C*/ so L:C is the
       true position of the character right after the comment.
check: verifies every /*line <rel>.orbit:L:C*/ directive points at .orbit text
       that matches the copied span (case/sep-insensitive for field keys).
Usage: tools/lines.py fix|check FILE.go...
"""
import os, re, sys

DIR = re.compile(r'/\*line ([^*]*?):(\d+):(\d+)\*/')

def fix(path):
    base = os.path.basename(path)
    out = []
    for line in open(path).read().split('\n'):
        # fixed point per line: directive lengths change column numbers
        for _ in range(10):
            new, pos = [], 0
            for m in DIR.finditer(line):
                new.append(line[pos:m.start()])
                if m.group(1) == base:
                    prefix = ''.join(new)
                    d = '/*line %s:%d:%d*/' % (base, len(out) + 1, 1)
                    col = len((prefix + d).encode()) + 1
                    d = '/*line %s:%d:%d*/' % (base, len(out) + 1, col)
                    col = len((prefix + d).encode()) + 1
                    d = '/*line %s:%d:%d*/' % (base, len(out) + 1, col)
                    new.append(d)
                else:
                    new.append(m.group(0))
                pos = m.end()
            new.append(line[pos:])
            nl = ''.join(new)
            if nl == line:
                break
            line = nl
        out.append(line)
    open(path, 'w').write('\n'.join(out))

def norm(s):
    return re.sub(r'[-_]', '', s).lower()

def check(path):
    ok = True
    src = open(path).read()
    for m in DIR.finditer(src):
        f = m.group(1)
        if not f.endswith('.orbit'):
            continue
        orbit = os.path.normpath(os.path.join(os.path.dirname(path), f))
        lines = open(orbit).read().split('\n')
        l, c = int(m.group(2)), int(m.group(3))
        rest = src[m.end():]
        span = rest[:rest.find('/*line')] if '/*line' in rest else rest
        span = span.split('\n')[0]
        target = lines[l - 1][c - 1:].encode()
        g = span
        if g.endswith('.Render'):            # component call -> tag name
            g = g[:-len('.Render')]
        if g == 'Render':
            g = 'Self'
        if g.startswith('"example.com/blog/orbit_gen/decl/'):  # import rewrite (span)
            g = '"'
        good = target.decode().startswith(g) or norm(target.decode()).startswith(norm(g))
        if not good:
            ok = False
        print('%s %s:%d:%d  go=%r  orbit=%r' % ('ok ' if good else 'BAD', f, l, c, span[:40], target.decode()[:40]))
    return ok

if __name__ == '__main__':
    cmd, files = sys.argv[1], sys.argv[2:]
    if cmd == 'fix':
        for p in files:
            fix(p)
    else:
        sys.exit(0 if all([check(p) for p in files]) else 1)
