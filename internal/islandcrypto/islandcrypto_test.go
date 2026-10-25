package islandcrypto

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func testKey(b byte) []byte { return bytes.Repeat([]byte{b}, KeySize) }

func newSealer(t testing.TB, b byte) *Sealer {
	t.Helper()
	s, err := New(testKey(b))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNewKeySize(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		if _, err := New(make([]byte, n)); err == nil {
			t.Errorf("New(%d bytes) succeeded", n)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	s := newSealer(t, 1)
	for _, props := range []string{"", "{}", `{"PostID":42}`, strings.Repeat("x", 9000)} {
		tok := s.Seal("Comments-9b1e44a2", "3f9a2c1d7e4b5a60", []byte(props))
		if strings.ContainsAny(tok, "+/=") {
			t.Errorf("token %q isn't base64url without padding", tok)
		}
		got, err := s.Open("Comments-9b1e44a2", "3f9a2c1d7e4b5a60", tok)
		if err != nil {
			t.Fatalf("Open(%q): %v", props, err)
		}
		if string(got) != props {
			t.Errorf("Open = %q, want %q", got, props)
		}
	}
}

func TestDeterministic(t *testing.T) {
	s1, s2 := newSealer(t, 1), newSealer(t, 1)
	a := s1.Seal("C-1", "b", []byte(`{"A":1}`))
	b := s2.Seal("C-1", "b", []byte(`{"A":1}`))
	if a != b {
		t.Errorf("same inputs gave %q and %q", a, b)
	}
	if c := s1.Seal("C-1", "b", []byte(`{"A":2}`)); c == a {
		t.Error("different props gave the same token")
	}
	if got := string(s1.AppendSeal([]byte("x="), "C-1", "b", []byte(`{"A":1}`))); got != "x="+a {
		t.Errorf("AppendSeal = %q, want %q", got, "x="+a)
	}
}

func TestRejects(t *testing.T) {
	s := newSealer(t, 1)
	const id, build = "Comments-9b1e44a2", "3f9a2c1d7e4b5a60"
	tok := s.Seal(id, build, []byte(`{"PostID":42}`))
	raw, _ := base64.RawURLEncoding.DecodeString(tok)
	flip := func(i int) string {
		b := bytes.Clone(raw)
		b[i] ^= 1
		return base64.RawURLEncoding.EncodeToString(b)
	}
	tests := []struct {
		name, id, build, tok string
		s                    *Sealer
	}{
		{"nonce flipped", id, build, flip(0), s},
		{"ciphertext flipped", id, build, flip(nonceSize + 1), s},
		{"tag flipped", id, build, flip(len(raw) - 1), s},
		{"other component", "Other-00000000", build, tok, s},
		{"other build", id, "0000000000000000", tok, s},
		// Length prefixes: moving a byte between the two IDs changes the ad.
		{"shifted ad", id + "3", build[1:], tok, s},
		{"other key", id, build, tok, newSealer(t, 2)},
		{"empty", id, build, "", s},
		{"short", id, build, tok[:20], s},
		{"truncated", id, build, tok[:len(tok)-1], s},
		{"padding", id, build, tok + "=", s},
		{"std alphabet", id, build, "++//" + tok[4:], s},
		{"not base64", id, build, "!!!!", s},
	}
	for _, tt := range tests {
		if _, err := tt.s.Open(tt.id, tt.build, tt.tok); err != ErrInvalid {
			t.Errorf("%s: Open err = %v, want ErrInvalid", tt.name, err)
		}
	}
}

// TestNonceCheck forges a token whose GCM tag is valid but whose nonce isn't
// the synthetic one: Open must reject it.
func TestNonceCheck(t *testing.T) {
	s := newSealer(t, 1)
	ad := appendAD(nil, "C-1", "b")
	nonce := make([]byte, nonceSize)
	raw := s.aead.Seal(append([]byte(nil), nonce...), nonce, []byte(`{}`), ad)
	tok := base64.RawURLEncoding.EncodeToString(raw)
	if _, err := s.Open("C-1", "b", tok); err != ErrInvalid {
		t.Errorf("Open of a forged nonce: err = %v, want ErrInvalid", err)
	}
}

func FuzzOpen(f *testing.F) {
	s := newSealer(f, 1)
	f.Add("C-1", "b", s.Seal("C-1", "b", []byte(`{"A":1}`)))
	f.Add("C-1", "b", "")
	f.Add("C-1", "0000000000000000", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	f.Fuzz(func(t *testing.T, id, build, tok string) {
		props, err := s.Open(id, build, tok)
		if err != nil {
			return
		}
		// Only tokens it sealed open: resealing gives the same token.
		if again := s.Seal(id, build, props); again != tok {
			t.Fatalf("Open accepted %q, which Seal wouldn't produce (%q)", tok, again)
		}
	})
}

func benchProps(n int) []byte {
	b := []byte(`{"Text":"`)
	b = append(b, strings.Repeat("a", n-len(b)-2)...)
	return append(b, `"}`...)
}

func BenchmarkSeal(b *testing.B) {
	s := newSealer(b, 1)
	for _, n := range []int{128, 1 << 10, 8 << 10} {
		props := benchProps(n)
		b.Run(sizeName(n), func(b *testing.B) {
			b.ReportAllocs()
			var dst []byte
			for b.Loop() {
				dst = s.AppendSeal(dst[:0], "Comments-9b1e44a2", "3f9a2c1d7e4b5a60", props)
			}
		})
	}
}

func BenchmarkOpen(b *testing.B) {
	s := newSealer(b, 1)
	for _, n := range []int{128, 1 << 10, 8 << 10} {
		tok := s.Seal("Comments-9b1e44a2", "3f9a2c1d7e4b5a60", benchProps(n))
		b.Run(sizeName(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := s.Open("Comments-9b1e44a2", "3f9a2c1d7e4b5a60", tok); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func sizeName(n int) string {
	switch n {
	case 128:
		return "128B"
	case 1 << 10:
		return "1KB"
	}
	return "8KB"
}
