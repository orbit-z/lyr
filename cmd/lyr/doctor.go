package main

import (
	"encoding/json"
	"fmt"

	"github.com/orbit-z/lyr/internal/doctor"
)

// doctorEnv returns the environment lyr doctor checks; tests replace it.
var doctorEnv = doctor.DefaultEnv

// runDoctor is lyr doctor [--json] (PLAN.md §6): exit code 1 on any ✗.
func runDoctor(e *env, args []string) int {
	c := lookup("doctor")
	fs := c.flagSet(e)
	asJSON := fs.Bool("json", false, "print the results as JSON")
	if code := parseNoArgs(e, c, fs, args); code >= 0 {
		return code
	}
	r := doctor.Run(e.ctx, doctorEnv(e.dir))
	if *asJSON {
		enc := json.NewEncoder(e.stdout)
		enc.SetEscapeHTML(false) // keep "=>" readable
		enc.SetIndent("", "  ")
		if err := enc.Encode(r); err != nil {
			fmt.Fprintln(e.stderr, "lyr doctor:", err)
			return exitFail
		}
	} else {
		r.WriteText(e.stdout)
	}
	if r.Failed() {
		return exitFail
	}
	return exitOK
}
