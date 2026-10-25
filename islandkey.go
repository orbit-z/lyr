package orbit

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bertrandkhe/orbit/internal/islandcrypto"
)

// Island key and build ID resolution (PLAN.md §4 "Island key", "Build ID").

// buildKey and buildID are set by orbit build with
// -ldflags=-X github.com/bertrandkhe/orbit.buildKey=… -X …buildID=…: a
// random island key (standard base64 of 32 bytes) and build ID (16 hex
// chars) per build. Both are empty in a plain go build or go run.
var (
	buildKey string
	buildID  string
)

// testBuildID is the build ID under go test (and in orbittest.Render).
const testBuildID = "0000000000000000"

// keyring is the island key and build ID a Site seals placeholders with.
type keyring struct {
	sealer  *islandcrypto.Sealer
	buildID string
	random  bool // the key is the per-process random one
}

// WithKey sets the 32-byte island key that seals server:defer props,
// overriding $ORBIT_KEY and the key embedded by orbit build. A key of
// another length panics.
func WithKey(key []byte) Option {
	if len(key) != islandcrypto.KeySize {
		panic("orbit: WithKey: the island key must be 32 bytes, got " + strconv.Itoa(len(key)))
	}
	k := append([]byte(nil), key...)
	return func(o *siteOptions) { o.key = k }
}

// resolveKeyring returns the keyring for a Site: WithKey's key if given,
// otherwise the process default (defaultKeyring).
func resolveKeyring(key []byte) *keyring {
	if key == nil {
		return defaultKeyring()
	}
	return &keyring{sealer: mustSealer(key), buildID: processBuildID()}
}

// envKeyring caches the keyring for one $ORBIT_KEY value, so RenderComponent
// can re-read the variable per call (as NewSite does) without re-deriving
// keys.
type envKeyring struct {
	env string
	kr  *keyring
}

var envCache atomic.Pointer[envKeyring]

// defaultKeyring is the key resolution without WithKey: $ORBIT_KEY, then
// the fixed test key under go test, then the key embedded by orbit build,
// then a random key for the process. An invalid $ORBIT_KEY panics.
func defaultKeyring() *keyring {
	env := os.Getenv("ORBIT_KEY")
	if env == "" {
		switch {
		case testing.Testing():
			return testKeyring()
		case buildKey != "":
			return embeddedKeyring()
		}
		return randomKeyring()
	}
	if c := envCache.Load(); c != nil && c.env == env {
		return c.kr
	}
	key, err := base64.StdEncoding.DecodeString(env)
	switch {
	case err != nil:
		panic("orbit: ORBIT_KEY isn't valid standard base64; it must be a 32-byte key as printed by \"orbit create-key\"")
	case len(key) != islandcrypto.KeySize:
		panic("orbit: ORBIT_KEY decodes to " + strconv.Itoa(len(key)) + " bytes; it must be a 32-byte key as printed by \"orbit create-key\"")
	}
	kr := &keyring{sealer: mustSealer(key), buildID: processBuildID()}
	envCache.Store(&envKeyring{env: env, kr: kr})
	return kr
}

// testKeyring is the fixed test key and build ID 0000000000000000, used
// under go test (unless ORBIT_KEY or WithKey is set) and always by
// orbittest.Render, so placeholders are the same on every run.
var testKeyring = sync.OnceValue(func() *keyring {
	var key [islandcrypto.KeySize]byte
	copy(key[:], "orbit fixed test island key 0000")
	return &keyring{sealer: mustSealer(key[:]), buildID: testBuildID}
})

var embeddedKeyring = sync.OnceValue(func() *keyring {
	key, err := base64.StdEncoding.DecodeString(buildKey)
	if err != nil || len(key) != islandcrypto.KeySize {
		panic("orbit: the island key embedded by orbit build (orbit.buildKey) isn't standard base64 of 32 bytes; rebuild with orbit build")
	}
	return &keyring{sealer: mustSealer(key), buildID: processBuildID()}
})

var randomKeyring = sync.OnceValue(func() *keyring {
	key := make([]byte, islandcrypto.KeySize)
	rand.Read(key)
	return &keyring{sealer: mustSealer(key), buildID: processBuildID(), random: true}
})

var warnRandomKey sync.Once

// warnIfRandom prints the one-line warning about a per-process key, once,
// for a site that has islands.
func (kr *keyring) warnIfRandom(islands int) {
	if !kr.random || islands == 0 {
		return
	}
	warnRandomKey.Do(func() {
		log.Print("orbit: no island key (ORBIT_KEY is unset and the binary wasn't built by orbit build); using a random key for this process, so other instances can't open its server:defer islands. Set ORBIT_KEY (see orbit create-key).")
	})
}

// processBuildID is the build ID: the one embedded by orbit build, or
// 0000000000000000 under go test, or random for the process.
var processBuildID = sync.OnceValue(func() string {
	switch {
	case buildID != "":
		if !isBuildID(buildID) {
			panic(fmt.Sprintf("orbit: the build ID embedded by orbit build (orbit.buildID=%q) must be 16 lowercase hex characters; rebuild with orbit build", buildID))
		}
		return buildID
	case testing.Testing():
		return testBuildID
	}
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
})

func isBuildID(s string) bool {
	if len(s) != 16 {
		return false
	}
	for i := range len(s) {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func mustSealer(key []byte) *islandcrypto.Sealer {
	s, err := islandcrypto.New(key)
	if err != nil {
		panic("orbit: " + err.Error())
	}
	return s
}
