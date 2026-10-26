package compiler

// The Go side of TestTSConformance: types and sample values for every row
// of the mapping table (PLAN.md §5c). This file is also type-checked on its
// own (standard library imports only) to get the samples' static types:
// every entry of tcSamples names a package-level slice variable whose
// element type is mapped, and whose elements are encoded with json.Marshal.

import (
	"encoding/json"
	"errors"
	"net/netip"
	"time"
)

type tcStatus string
type tcIDs []int
type tcBlob []byte

type tcPost struct {
	ID        int64     `json:"id"`
	Slug      string    `json:"slug"`
	Tags      []string  `json:"tags"`
	Published time.Time `json:"published"`
	Author    *tcAuthor `json:"author,omitempty"`
	draft     bool
}

type tcAuthor struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type tcJM struct{ X int }

func (tcJM) MarshalJSON() ([]byte, error) { return []byte(`[1,"two"]`), nil }

type tcPJM struct{ X int }

func (*tcPJM) MarshalJSON() ([]byte, error) { return []byte(`"custom"`), nil }

type tcTM int

func (t tcTM) MarshalText() ([]byte, error) { return []byte("tm" + string(rune('0'+t))), nil }

type tcPTM struct{ X int }

func (*tcPTM) MarshalText() ([]byte, error) { return []byte("ptm"), nil }

type tcQ struct {
	I  int         `json:"i,string"`
	P  *int        `json:"p,string"`
	B  bool        `json:",string"`
	S  string      `json:"s,string"`
	F  float64     `json:"f,string"`
	T  time.Time   `json:"t,string"`
	X  []int       `json:"x,string"`
	N  tcTM        `json:"n,string"`
	JN json.Number `json:"jn,string"`
	PS *tcStatus   `json:"ps,string,omitempty"`
}

type tcZeroSlice []int

func (z tcZeroSlice) IsZero() bool { return len(z) == 1 }

type tcO struct {
	S    string         `json:"s,omitempty"`
	P    *int           `json:"p,omitempty"`
	L    []int          `json:"l,omitempty"`
	M    map[string]int `json:"m,omitempty"`
	St   tcAuthor       `json:"st,omitempty"`
	PSt  *tcAuthor      `json:"pst,omitempty"`
	A0   [0]int         `json:"a0,omitempty"`
	A2   [2]int         `json:"a2,omitempty"`
	NL   tcIDs          `json:"nl,omitempty"`
	ZS   tcAuthor       `json:"zs,omitzero"`
	ZP   *int           `json:"zp,omitzero"`
	ZL   []int          `json:"zl,omitzero"`
	ZT   time.Time      `json:"zt,omitzero"`
	ZI   tcZeroSlice    `json:"zi,omitzero"`
	Dash int            `json:"-,"`
	Skip int            `json:"-"`
	Fn   func()         `json:"-"`
	Bad  int            `json:"a'b"`
	Hy   int            `json:"my-field"`
	Opt  int            `json:",omitempty"`
	fn   func()
}

type tcBase struct {
	ID   int `json:"id"`
	Name string
}
type tcOther struct {
	Name  string
	Extra int
}
type tcTagged struct {
	Name string `json:"Name"`
}
type tcDeep struct{ tcBase }
type tcStatusInt int
type tcE7inner struct{ Hidden int }

type tcE1 struct {
	tcBase
	tcOther
}
type tcE2 struct {
	tcBase
	tcTagged
}
type tcE3 struct {
	Name string
	tcBase
}
type tcE4 struct {
	*tcBase
	X int
}
type tcE5 struct {
	tcBase `json:"base"`
}
type tcE6 struct {
	TcStatus
	*TcIDs
}
type TcStatus string
type TcIDs []int
type tcE7 struct {
	tcE7inner
	tcStatusInt
}
type tcE8 struct {
	tcDeep
	tcOther
}
type tcE9 struct {
	time.Time
}

type tcPage[T any] struct {
	Items []T        `json:"items"`
	Next  *tcPage[T] `json:"next"`
}
type tcPair[K comparable, V any] struct {
	K K
	V V
}
type tcList[T any] []T
type tcG[T any] struct {
	X T `json:"x,omitempty"`
	Y T `json:"y,string"`
}

type tcNode struct {
	Kids   []tcNode `json:"kids"`
	Parent *tcNode  `json:"parent"`
}

func tcPtr[T any](v T) *T { return &v }

var (
	tcBool      = []bool{false, true}
	tcInt       = []int{0, -7, 1 << 40}
	tcInt64     = []int64{-9223372036854775808}
	tcUint64    = []uint64{18446744073709551615}
	tcUintptr   = []uintptr{0, 42}
	tcFloat64   = []float64{0, 1.5, 1e21, -1e-7}
	tcFloat32   = []float32{0.1}
	tcString    = []string{"", "a <b> & 'c' \"d\"   é"}
	tcBytes     = [][]byte{nil, {}, {0, 1, 255}}
	tcByteArr   = [][3]byte{{1, 2, 3}}
	tcBlobs     = []tcBlob{nil, tcBlob("hi")}
	tcStrings   = [][]string{nil, {}, {"a", "b"}}
	tcNested    = [][][]int{nil, {nil, {1}}}
	tcArr       = [][2]int{{1, 2}}
	tcPtrs      = [][]*int{{nil, tcPtr(1)}}
	tcMap       = []map[string]int{nil, {}, {"a": 1}}
	tcIntMap    = []map[int]string{{1: "a", -2: "b"}}
	tcTMMap     = []map[tcTM]int{{tcTM(1): 1}}
	tcPtrInt    = []*int{nil, tcPtr(3)}
	tcPtrPtr    = []**int{nil, tcPtr((*int)(nil)), tcPtr(tcPtr(4))}
	tcAny       = []any{nil, 1, "s", []int{1}, map[string]any{"a": nil}}
	tcErr       = []error{nil, errors.New("x")}
	tcTime      = []time.Time{{}, time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	tcPtrTime   = []*time.Time{nil, tcPtr(time.Date(2026, 1, 2, 3, 4, 5, 6, time.FixedZone("x", 3600)))}
	tcDuration  = []time.Duration{0, time.Second}
	tcNumber    = []json.Number{"12", "-1.5e3"}
	tcRaw       = []json.RawMessage{nil, json.RawMessage(`{"a":[1,"x"]}`)}
	tcAddr      = []netip.Addr{{}, netip.MustParseAddr("192.168.0.1"), netip.MustParseAddr("::1")}
	tcPtrAddr   = []*netip.Addr{nil, tcPtr(netip.MustParseAddr("10.0.0.1"))}
	tcJMs       = []tcJM{{}}
	tcPtrJM     = []*tcJM{nil, {}}
	tcPJMs      = []tcPJM{{X: 1}}
	tcPtrPJM    = []*tcPJM{nil, {}}
	tcTMs       = []tcTM{1}
	tcPtrTM     = []*tcTM{nil, tcPtr(tcTM(2))}
	tcPTMs      = []tcPTM{{X: 1}}
	tcPtrPTM    = []*tcPTM{nil, {}}
	tcAnon      = []struct{ A, b int }{{A: 1}}
	tcEmpty     = []struct{}{{}}
	tcStatuses  = []tcStatus{"draft"}
	tcIDList    = []tcIDs{nil, {1}}
	tcPosts     = []tcPost{{}, {ID: 1, Slug: "s", Tags: []string{"go"}, Author: &tcAuthor{Name: "n", URL: "u"}}}
	tcPostPtrs  = []*tcPost{nil, {Tags: []string{}}}
	tcQs        = []tcQ{{}, {I: 1, P: tcPtr(2), B: true, S: "s", F: 1.5, X: []int{1}, N: 3, JN: "7", PS: tcPtr(tcStatus("x"))}}
	tcOs        = []tcO{{}, {S: "s", P: tcPtr(0), L: []int{}, M: map[string]int{}, PSt: &tcAuthor{}, NL: tcIDs{}, ZS: tcAuthor{Name: "z"}, ZP: tcPtr(0), ZL: []int{}, ZT: time.Unix(0, 0).UTC(), ZI: tcZeroSlice{}, Dash: 1, Bad: 2, Hy: 3, Opt: 4}, {ZI: tcZeroSlice{1}}}
	tcE1s       = []tcE1{{tcBase{1, "a"}, tcOther{"b", 2}}}
	tcE2s       = []tcE2{{tcBase{1, "a"}, tcTagged{"t"}}}
	tcE3s       = []tcE3{{"top", tcBase{1, "a"}}}
	tcE4s       = []tcE4{{nil, 1}, {&tcBase{2, "b"}, 3}}
	tcE5s       = []tcE5{{tcBase{1, "a"}}}
	tcE6s       = []tcE6{{"s", nil}, {"t", &TcIDs{1}}}
	tcE7s       = []tcE7{{tcE7inner{1}, 2}}
	tcE8s       = []tcE8{{tcDeep{tcBase{1, "deep"}}, tcOther{"other", 2}}}
	tcE9s       = []tcE9{{}}
	tcPages     = []tcPage[tcPost]{{}, {Items: []tcPost{{ID: 1}}, Next: &tcPage[tcPost]{Items: []tcPost{}}}}
	tcPairs     = []tcPair[string, *tcStatus]{{K: "k"}, {K: "k", V: tcPtr(tcStatus("v"))}}
	tcLists     = []tcList[int]{nil, {1, 2}}
	tcGs        = []tcG[int]{{}, {X: 1, Y: 2}}
	tcGStrs     = []tcG[[]string]{{}, {X: []string{"a"}}}
	tcNodes     = []tcNode{{}, {Kids: []tcNode{{Parent: &tcNode{}}}}}
	tcNilPosts  = [][]tcPost{nil, {}}
	tcMapPosts  = []map[string]*tcPost{{"a": nil, "b": {}}}
	tcAnonPosts = []struct {
		Post  tcPost `json:"post"`
		Liked bool   `json:"liked"`
	}{{Liked: true}}
)

// tcSamples lists the sample variables by name (the test looks their
// static types up in the type-checked file).
var tcSamples = map[string]any{
	"tcBool": tcBool, "tcInt": tcInt, "tcInt64": tcInt64, "tcUint64": tcUint64, "tcUintptr": tcUintptr,
	"tcFloat64": tcFloat64, "tcFloat32": tcFloat32, "tcString": tcString, "tcBytes": tcBytes,
	"tcByteArr": tcByteArr, "tcBlobs": tcBlobs, "tcStrings": tcStrings, "tcNested": tcNested, "tcArr": tcArr,
	"tcPtrs": tcPtrs, "tcMap": tcMap, "tcIntMap": tcIntMap, "tcTMMap": tcTMMap, "tcPtrInt": tcPtrInt,
	"tcPtrPtr": tcPtrPtr, "tcAny": tcAny, "tcErr": tcErr, "tcTime": tcTime, "tcPtrTime": tcPtrTime,
	"tcDuration": tcDuration, "tcNumber": tcNumber, "tcRaw": tcRaw, "tcAddr": tcAddr, "tcPtrAddr": tcPtrAddr,
	"tcJMs": tcJMs, "tcPtrJM": tcPtrJM, "tcPJMs": tcPJMs, "tcPtrPJM": tcPtrPJM, "tcTMs": tcTMs,
	"tcPtrTM": tcPtrTM, "tcPTMs": tcPTMs, "tcPtrPTM": tcPtrPTM, "tcAnon": tcAnon, "tcEmpty": tcEmpty,
	"tcStatuses": tcStatuses, "tcIDList": tcIDList, "tcPosts": tcPosts, "tcPostPtrs": tcPostPtrs,
	"tcQs": tcQs, "tcOs": tcOs, "tcE1s": tcE1s, "tcE2s": tcE2s, "tcE3s": tcE3s, "tcE4s": tcE4s,
	"tcE5s": tcE5s, "tcE6s": tcE6s, "tcE7s": tcE7s, "tcE8s": tcE8s, "tcE9s": tcE9s, "tcPages": tcPages,
	"tcPairs": tcPairs, "tcLists": tcLists, "tcGs": tcGs, "tcGStrs": tcGStrs, "tcNodes": tcNodes,
	"tcNilPosts": tcNilPosts, "tcMapPosts": tcMapPosts, "tcAnonPosts": tcAnonPosts,
}

// tcNegative are JSON texts the TS type of a sample variable must reject.
var tcNegative = map[string][]string{
	"tcBool":      {`1`, `null`, `"true"`},
	"tcInt":       {`"1"`, `null`},
	"tcString":    {`1`, `null`},
	"tcBytes":     {`[1,2]`},
	"tcByteArr":   {`"AQID"`, `null`},
	"tcBlobs":     {`[1]`},
	"tcStrings":   {`[1]`, `[null]`},
	"tcNested":    {`[[null]]`},
	"tcArr":       {`null`},
	"tcPtrs":      {`[["1"]]`},
	"tcMap":       {`{"a":"x"}`, `[]`},
	"tcPtrInt":    {`"1"`},
	"tcTime":      {`1`, `null`},
	"tcPtrTime":   {`1`},
	"tcDuration":  {`"1s"`},
	"tcNumber":    {`"12"`, `null`},
	"tcAddr":      {`null`, `[192,168,0,1]`},
	"tcTMs":       {`1`, `null`},
	"tcPtrTM":     {`1`},
	"tcPtrPTM":    {`{"X":1}`},
	"tcAnon":      {`{"A":1,"b":2}`, `{}`},
	"tcStatuses":  {`1`},
	"tcIDList":    {`["1"]`},
	"tcPosts":     {`{"id":1,"slug":"","tags":null,"published":"","draft":false}`, `{"id":"1","slug":"","tags":null,"published":""}`, `{"slug":"","tags":null,"published":""}`, `{"id":1,"slug":"","tags":null,"published":"","author":null}`},
	"tcPostPtrs":  {`{"id":1}`},
	"tcQs":        {`{"i":1,"p":null,"B":"false","s":"\"\"","f":"0","t":"","x":null,"n":"","jn":""}`, `{"i":"0","p":null,"B":false,"s":"\"\"","f":"0","t":"","x":null,"n":"","jn":""}`, `{"i":"0","p":null,"B":"false","s":"\"\"","f":"0","t":"","x":null,"n":"","jn":"","ps":null}`},
	"tcOs":        {`{"st":{"name":""},"a2":[0,0],"-":0,"a":0,"my-field":0,"l":null}`, `{"st":{"name":""},"a2":[0,0],"-":0,"a":0,"my-field":0,"Dash":0}`, `{"a2":[0,0],"-":0,"a":0,"my-field":0}`, `{"st":{"name":""},"a2":[0,0],"-":0,"a":0,"my-field":0,"zp":null}`},
	"tcE1s":       {`{"id":1,"Extra":2,"Name":"x"}`},
	"tcE2s":       {`{"id":1}`},
	"tcE4s":       {`{"id":null,"X":1}`},
	"tcE5s":       {`{"id":1,"Name":"a"}`},
	"tcE6s":       {`{"TcStatus":"s"}`},
	"tcE7s":       {`{"Hidden":1,"tcStatusInt":2}`},
	"tcE8s":       {`{"id":1,"Name":"x","Extra":2,"tcDeep":{}}`},
	"tcPages":     {`{"items":[{"id":"x"}],"next":null}`, `{"items":null}`},
	"tcPairs":     {`{"K":1,"V":null}`},
	"tcLists":     {`["1"]`},
	"tcGs":        {`{"x":"s","y":"1"}`},
	"tcNodes":     {`{"kids":[{"kids":1,"parent":null}],"parent":null}`},
	"tcMapPosts":  {`{"a":{"id":1}}`},
	"tcAnonPosts": {`{"liked":true}`},
}
