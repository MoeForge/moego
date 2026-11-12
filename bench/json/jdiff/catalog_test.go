package jdiff

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The catalog holds named types that random types use as leaves or
// embed: types with MarshalJSON, UnmarshalJSON, MarshalText, UnmarshalText
// and IsZero methods on value and pointer receivers, structs with
// embedding, unexported fields and tag conflicts, and recursive types.
// Methods fail for some values, so that error paths are compared too.

var errMethod = errors.New("method failed")

// VMarsh has MarshalJSON on the value receiver.
type VMarsh int

func (v VMarsh) MarshalJSON() ([]byte, error) {
	switch v % 7 {
	case 0:
		return []byte(` { "v" : ` + strconv.Itoa(int(v)) + ` , "s" : "<&>" } `), nil
	case 1:
		return []byte(`"` + strconv.Itoa(int(v)) + `"`), nil
	case 2:
		return nil, errMethod
	case 3:
		return []byte(`{"bad"`), nil
	case 4:
		return []byte("[1,\n 2]"), nil
	case 5:
		return []byte(`"  é"`), nil
	}
	return []byte(strconv.Itoa(int(v))), nil
}

// PMarsh has MarshalJSON and UnmarshalJSON on the pointer receiver.
type PMarsh struct {
	N int
	S string
}

func (p *PMarsh) MarshalJSON() ([]byte, error) {
	if p.N%5 == 3 {
		return nil, errMethod
	}
	return []byte(fmt.Sprintf(`{"n":%d,"s":%q}`, p.N, p.S)), nil
}

func (p *PMarsh) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return errMethod
	}
	var m struct {
		N int
		S string
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	p.N, p.S = m.N+1, m.S+"!"
	return nil
}

// Capture keeps the bytes passed to UnmarshalJSON.
type Capture struct{ Raw string }

func (c *Capture) UnmarshalJSON(b []byte) error {
	c.Raw = string(b)
	return nil
}

// VText has MarshalText on the value receiver and UnmarshalText on the
// pointer receiver.
type VText struct{ A, B string }

func (t VText) MarshalText() ([]byte, error) {
	if t.A == "err" {
		return nil, errMethod
	}
	return []byte(t.A + "|" + t.B), nil
}

func (t *VText) UnmarshalText(b []byte) error {
	a, c, ok := strings.Cut(string(b), "|")
	if !ok {
		return errMethod
	}
	t.A, t.B = a, c
	return nil
}

// PText has both text methods on the pointer receiver.
type PText int

func (t *PText) MarshalText() ([]byte, error) {
	if *t == 13 {
		return nil, errMethod
	}
	return []byte("t" + strconv.Itoa(int(*t))), nil
}

func (t *PText) UnmarshalText(b []byte) error {
	n, err := strconv.Atoi(strings.TrimPrefix(string(b), "t"))
	*t = PText(n)
	return err
}

// KeyInt is an integer map key with text methods, which take precedence.
type KeyInt int

func (k KeyInt) MarshalText() ([]byte, error) { return []byte("k" + strconv.Itoa(int(k))), nil }

func (k *KeyInt) UnmarshalText(b []byte) error {
	n, err := strconv.Atoi(strings.TrimPrefix(string(b), "k"))
	*k = KeyInt(n)
	return err
}

// KeyStr is a string map key kind with a name.
type KeyStr string

// Both has JSON and text methods; the JSON ones win.
type Both string

func (b Both) MarshalJSON() ([]byte, error) { return []byte(`{"both":"` + string(b) + `"}`), nil }
func (b Both) MarshalText() ([]byte, error) { return []byte("text:" + string(b)), nil }
func (b *Both) UnmarshalJSON(d []byte) error {
	*b = Both("json:" + string(d))
	return nil
}
func (b *Both) UnmarshalText(d []byte) error {
	*b = Both("text:" + string(d))
	return nil
}

// ZeroV has IsZero on the value receiver; ZeroP on the pointer receiver.
type ZeroV struct{ N int }

func (z ZeroV) IsZero() bool { return z.N <= 1 }

type ZeroP struct{ N int }

func (z *ZeroP) IsZero() bool { return z == nil || z.N == 2 }

// Base is embedded by the structs below.
type Base struct {
	ID    int    `json:"id"`
	Name  string `json:"name,omitempty"`
	inner int
	Dup   int
}

type Other struct {
	Dup   int
	Name  string
	Extra *int `json:",omitempty"`
}

type unexp struct {
	U int
	V string `json:"v"`
}

// Outer embeds by value and pointer, exported and unexported, with
// conflicts that cancel and tags that win.
type Outer struct {
	Base
	*Other
	unexp
	Own  string  `json:"own"`
	Dup  int     `json:"dup"`
	Skip int     `json:"-"`
	Dash int     `json:"-,"`
	Q    int64   `json:"q,string"`
	QF   float64 `json:"qf,string"`
	QB   bool    `json:"qb,string"`
	QS   string  `json:"qs,string"`
	QP   *int    `json:"qp,string"`
	priv int
}

// Inner is embedded by random structs; its fields collide with Base's.
type Inner struct {
	ID   string `json:"id,omitempty"`
	Kids []int  `json:"kids"`
	unexp
}

// PtrUnexp embeds a pointer to an unexported struct, which decoding can
// not allocate.
type PtrUnexp struct {
	*unexp
	X int
}

// Node is recursive.
type Node struct {
	V    int              `json:"v"`
	Next *Node            `json:"next,omitempty"`
	Kids []Node           `json:"kids,omitempty"`
	M    map[string]*Node `json:"m,omitempty"`
}

// Tagged has unusual tags.
type Tagged struct {
	A int       `json:"a,omitempty,omitzero"`
	B int       `json:"bad name!"`
	C int       `json:",string,omitempty"`
	D string    `json:"D"`
	E string    `json:"d"`
	F any       `json:"f,omitempty"`
	G []byte    `json:"g,omitzero"`
	H ZeroV     `json:"h,omitzero"`
	I ZeroP     `json:"i,omitzero"`
	J *ZeroP    `json:"j,omitzero"`
	K time.Time `json:"k,omitzero"`
	L [2]int    `json:"l,omitzero"`
	M float64   `json:"m,omitzero"`
}

// Quoted has the ",string" option on every kind of field, including those
// that ignore it.
type Quoted struct {
	I   int         `json:",string"`
	PI  *int        `json:",string"`
	PPI **int       `json:",string"`
	PS  *string     `json:",string"`
	S   string      `json:",string"`
	B   *bool       `json:",string"`
	F   float32     `json:",string"`
	U   uint8       `json:",string"`
	T   PText       `json:",string"`
	PT  *PText      `json:",string"`
	J   Both        `json:",string"`
	V   VMarsh      `json:",string"`
	N   json.Number `json:",string"`
	A   any         `json:",string"`
	K   KeyStr      `json:",string"`
	L   []int       `json:",string"`
	M   MyByte      `json:",string"`
	PN  *Node       `json:",string"`
}

// Stringer is a non-empty interface for interface-typed fields.
type Stringer interface{ String() string }

type StrVal string

func (s StrVal) String() string { return string(s) }

// Ifaces has interface-typed fields.
type Ifaces struct {
	Any  any
	Str  Stringer
	M    json.Marshaler
	T    interface{ MarshalText() ([]byte, error) }
	PAny *any
}

// Bytes are byte types.
type MyBytes []byte
type MyByte byte

// catalog lists the named types random types may use.
var catalog = []any{
	VMarsh(0), PMarsh{}, Capture{}, VText{}, PText(0), Both(""), ZeroV{}, ZeroP{},
	Base{}, Other{}, Outer{}, PtrUnexp{}, Node{}, Tagged{}, Ifaces{}, StrVal(""), Quoted{}, Inner{},
	MyBytes(nil), []MyByte(nil), [3]byte{}, time.Time{}, time.Duration(0),
	json.Number(""), json.RawMessage(nil), KeyInt(0), KeyStr(""),
	map[KeyInt]int(nil), map[KeyStr]string(nil), map[VText]int(nil), map[PText]int(nil), map[int8]bool(nil), map[uint16]string(nil),
	map[string]VMarsh(nil), map[string]*PMarsh(nil), []VText(nil), []*PText(nil), [2]PText{},
}
