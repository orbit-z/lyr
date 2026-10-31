package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ReadModule returns the module path declared in dir/go.mod.
func ReadModule(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", err
	}
	return ModulePath(data)
}

// ModulePath returns the path of the module directive in a go.mod file. It
// understands // comments, quoted ("…" and `…`) paths and the block form
// module ( path ), which is all the go.mod grammar allows for it.
func ModulePath(gomod []byte) (string, error) {
	inBlock := false
	for n, line := range strings.Split(string(gomod), "\n") {
		toks, err := modTokens(line)
		if err != nil {
			return "", fmt.Errorf("go.mod:%d: %v", n+1, err)
		}
		if len(toks) == 0 {
			continue
		}
		if inBlock {
			if toks[0] == ")" {
				inBlock = false
				continue
			}
			return modPath(toks[0], n+1)
		}
		if toks[0] != "module" {
			continue
		}
		switch {
		case len(toks) == 2 && toks[1] == "(":
			inBlock = true
		case len(toks) == 4 && toks[1] == "(" && toks[3] == ")":
			return modPath(toks[2], n+1)
		case len(toks) == 2:
			return modPath(toks[1], n+1)
		default:
			return "", fmt.Errorf("go.mod:%d: usage: module module/path", n+1)
		}
	}
	return "", errors.New("go.mod: no module directive")
}

func modPath(tok string, line int) (string, error) {
	if tok[0] == '"' || tok[0] == '`' {
		p, err := strconv.Unquote(tok)
		if err != nil {
			return "", fmt.Errorf("go.mod:%d: invalid quoted module path %s", line, tok)
		}
		tok = p
	}
	if tok == "" {
		return "", fmt.Errorf("go.mod:%d: empty module path", line)
	}
	return tok, nil
}

// modTokens splits a go.mod line into tokens: words, quoted strings, and the
// block delimiters "(" and ")", dropping a trailing // comment.
func modTokens(line string) ([]string, error) {
	var toks []string
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case strings.HasPrefix(line[i:], "//"):
			return toks, nil
		case c == '(' || c == ')':
			toks = append(toks, line[i:i+1])
			i++
		case c == '"' || c == '`':
			j := i + 1
			for j < len(line) && line[j] != c {
				if c == '"' && line[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(line) {
				return nil, errors.New("unterminated quoted string")
			}
			toks = append(toks, line[i:j+1])
			i = j + 1
		default:
			j := i
			for j < len(line) && !strings.ContainsRune(" \t\r()\"`", rune(line[j])) && !strings.HasPrefix(line[j:], "//") {
				j++
			}
			toks = append(toks, line[i:j])
			i = j
		}
	}
	return toks, nil
}

// FindRoot returns the nearest directory at or above dir that has a go.mod.
func FindRoot(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for d := dir; ; {
		if fi, err := os.Stat(filepath.Join(d, "go.mod")); err == nil && !fi.IsDir() {
			return d, nil
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("no go.mod in %s or any parent directory; run lyr inside a Go module", dir)
		}
		d = parent
	}
}
