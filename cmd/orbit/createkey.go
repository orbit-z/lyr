package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// runCreateKey is orbit create-key: print a new random 32-byte island key in
// standard base64, for ORBIT_KEY (PLAN.md §6).
func runCreateKey(e *env, args []string) int {
	c := lookup("create-key")
	fs := c.flagSet(e)
	if code := parseNoArgs(e, c, fs, args); code >= 0 {
		return code
	}
	key := make([]byte, 32)
	rand.Read(key)
	fmt.Fprintln(e.stdout, base64.StdEncoding.EncodeToString(key))
	return exitOK
}
