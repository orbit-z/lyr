package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// -tailwind (PLAN.md §5d): the project uses Tailwind CSS. A global.css
// root imported from the Base layout, twelve utilities added to every
// .orbit file (as the spike did), and a .gitignore that keeps orbit_gen/,
// .orbit/ and the binaries out of Tailwind's scan. The CLI must be on PATH.

var twUtilities = []string{"flex", "grid", "items-center", "justify-between", "gap-{n}", "p-{n}", "px-{n}", "py-{n}", "m-{n}", "mt-{n}",
	"text-sm", "text-lg", "font-bold", "rounded-md", "shadow-sm", "bg-{c}-{s}", "text-{c}-{s}", "border", "border-{c}-{s}", "hover:bg-{c}-{s}",
	"md:flex-row", "lg:grid-cols-{k}", "w-full", "max-w-{z}", "truncate", "leading-tight", "tracking-wide", "dark:bg-{c}-{s}", "focus-visible:ring-{k}", "transition-colors"}

var (
	twColors  = []string{"red", "blue", "green", "slate", "amber", "indigo", "rose", "teal", "sky", "zinc"}
	twShades  = []int{100, 200, 300, 500, 700, 900}
	twSizes   = []string{"sm", "md", "lg", "xl", "2xl"}
	firstCls  = regexp.MustCompile(`class="(c\d+)"`)
	slotTagRE = regexp.MustCompile(`<slot />`)
)

func twClasses(i int) string {
	var cls []string
	for j := range 12 {
		u := twUtilities[(i*7+j*3)%len(twUtilities)]
		u = strings.NewReplacer(
			"{n}", fmt.Sprint((i+j)%12+1),
			"{c}", twColors[(i+j)%len(twColors)],
			"{s}", fmt.Sprint(twShades[(i*j)%len(twShades)]),
			"{k}", fmt.Sprint((i+j)%4+1),
			"{z}", twSizes[(i+j)%5],
		).Replace(u)
		cls = append(cls, u)
	}
	return strings.Join(cls, " ")
}

func tailwindSetup(dir string) error {
	if err := writeFile(dir, ".gitignore", "orbit_gen/\n.orbit/\ndist/\nnode_modules/\n.bin/\n"); err != nil {
		return err
	}
	if err := writeFile(dir, "src/styles/global.css", "@import \"tailwindcss\";\n"); err != nil {
		return err
	}
	var files []string
	filepath.WalkDir(filepath.Join(dir, "src"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".orbit") {
			files = append(files, p)
		}
		return nil
	})
	slices.Sort(files)
	for i, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		s, cls := string(data), twClasses(i)
		if loc := firstCls.FindStringSubmatchIndex(s); loc != nil {
			s = s[:loc[1]-1] + " " + cls + s[loc[1]-1:]
		} else if loc := slotTagRE.FindStringIndex(s); loc != nil {
			s = s[:loc[0]] + `<div class="` + cls + `"><slot /></div>` + s[loc[1]:]
		}
		if strings.HasSuffix(p, filepath.FromSlash("src/layouts/Base.orbit")) {
			s += "\n<style is:global>\n  @import \"../styles/global.css\";\n</style>\n"
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			return err
		}
	}
	return nil
}
