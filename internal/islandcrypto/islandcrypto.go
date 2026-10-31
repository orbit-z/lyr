// Package islandcrypto seals the props of server:defer islands (PLAN.md
// §3.4 "Sealing"): a deterministic, synthetic-nonce construction over stdlib
// primitives.
//
//   - Two 32-byte keys are derived from the master key with HKDF-SHA256, with
//     the info strings "lyr island mac" and "lyr island enc".
//   - nonce = HMAC-SHA256(macKey, lp(componentID) ‖ lp(buildID) ‖ props)[:12],
//     where lp is a 4-byte big-endian length prefix.
//   - token = base64url-nopad(nonce ‖ AES-256-GCM(encKey).Seal(nonce, props, ad)),
//     with ad = lp(componentID) ‖ lp(buildID).
//
// Opening verifies the GCM tag, then recomputes the nonce from the plaintext
// and compares it in constant time. The same inputs always give the same
// token; a nonce repeats only for identical inputs, which give identical
// ciphertexts.
package islandcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash"
	"strings"
	"sync"
)

// KeySize is the size of a master island key.
const KeySize = 32

const (
	nonceSize = 12
	tagSize   = 16
)

// ErrInvalid is returned by Open for a token that is malformed, tampered
// with, sealed with another key, or sealed for another component or build.
var ErrInvalid = errors.New("islandcrypto: invalid token")

// enc is strict, so a token has a single encoding (Open also rejects CR and
// LF, which base64 decoding skips).
var enc = base64.RawURLEncoding.Strict()

// Sealer seals and opens island tokens with one master key. It is safe for
// concurrent use.
type Sealer struct {
	aead     cipher.AEAD
	scratchs sync.Pool // of *scratch
}

// scratch is per-call working memory, pooled so that sealing allocates
// nothing beyond the output.
type scratch struct {
	mac   hash.Hash // HMAC-SHA256 keyed with the mac key
	ad    []byte
	sum   [sha256.Size]byte
	nonce [nonceSize]byte
	raw   []byte // nonce ‖ ciphertext ‖ tag, when sealing
}

// maxPooledRaw is the largest raw buffer kept in the pool.
const maxPooledRaw = 64 << 10

// New returns a Sealer for a 32-byte master key.
func New(key []byte) (*Sealer, error) {
	if len(key) != KeySize {
		return nil, errors.New("islandcrypto: the key must be 32 bytes")
	}
	macKey, err := hkdf.Key(sha256.New, key, nil, "lyr island mac", 32)
	if err != nil {
		return nil, err
	}
	encKey, err := hkdf.Key(sha256.New, key, nil, "lyr island enc", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	s := &Sealer{aead: aead}
	s.scratchs.New = func() any {
		return &scratch{mac: hmac.New(sha256.New, macKey), ad: make([]byte, 0, 64)}
	}
	return s, nil
}

func (s *Sealer) get(componentID, buildID string) *scratch {
	sc := s.scratchs.Get().(*scratch)
	sc.ad = appendAD(sc.ad[:0], componentID, buildID)
	return sc
}

func (s *Sealer) put(sc *scratch) {
	if cap(sc.raw) > maxPooledRaw {
		sc.raw = nil
	}
	s.scratchs.Put(sc)
}

// appendAD appends lp(componentID) ‖ lp(buildID) to b.
func appendAD(b []byte, componentID, buildID string) []byte {
	b = binary.BigEndian.AppendUint32(b, uint32(len(componentID)))
	b = append(b, componentID...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(buildID)))
	return append(b, buildID...)
}

// computeNonce sets sc.nonce to the synthetic nonce of sc.ad and props.
func (sc *scratch) computeNonce(props []byte) {
	sc.mac.Reset()
	sc.mac.Write(sc.ad)
	sc.mac.Write(props)
	sc.mac.Sum(sc.sum[:0])
	copy(sc.nonce[:], sc.sum[:nonceSize])
}

// Seal returns the token for props sealed for componentID and buildID.
func (s *Sealer) Seal(componentID, buildID string, props []byte) string {
	return string(s.AppendSeal(nil, componentID, buildID, props))
}

// AppendSeal appends the token for props, sealed for componentID and
// buildID, to dst and returns the extended slice.
func (s *Sealer) AppendSeal(dst []byte, componentID, buildID string, props []byte) []byte {
	sc := s.get(componentID, buildID)
	sc.computeNonce(props)
	raw := append(sc.raw[:0], sc.nonce[:]...)
	raw = s.aead.Seal(raw, sc.nonce[:], props, sc.ad)
	dst = enc.AppendEncode(dst, raw)
	sc.raw = raw
	s.put(sc)
	return dst
}

// Open returns the props sealed in token for componentID and buildID, or
// ErrInvalid. The returned slice is newly allocated.
func (s *Sealer) Open(componentID, buildID, token string) ([]byte, error) {
	if strings.ContainsAny(token, "\r\n") {
		return nil, ErrInvalid
	}
	raw := make([]byte, enc.DecodedLen(len(token)))
	n, err := enc.Decode(raw, []byte(token))
	if err != nil || n < nonceSize+tagSize {
		return nil, ErrInvalid
	}
	raw = raw[:n]
	sc := s.get(componentID, buildID)
	defer s.put(sc)
	nonce, ct := raw[:nonceSize], raw[nonceSize:]
	// Decrypt in place: the plaintext overwrites the ciphertext.
	props, err := s.aead.Open(ct[:0], nonce, ct, sc.ad)
	if err != nil {
		return nil, ErrInvalid
	}
	sc.computeNonce(props)
	if subtle.ConstantTimeCompare(sc.nonce[:], nonce) != 1 {
		return nil, ErrInvalid
	}
	return props, nil
}
