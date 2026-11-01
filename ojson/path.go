package ojson

// Paths are dot-separated components. A backslash escapes the next byte, so
// `a\.b` names the key "a.b". A trailing lone backslash is dropped.

// nextComp returns the raw component starting at path[i], the index of the
// next component, or -1 when this is the last one, and whether the component
// contains escapes.
func nextComp(path string, i int) (raw string, next int, esc bool) {
	for j := i; j < len(path); j++ {
		switch path[j] {
		case '\\':
			esc = true
			j++
		case '.':
			return path[i:j], j + 1, esc
		}
	}
	return path[i:], -1, esc
}

// Classes of path bytes for getComp.
const (
	pathDot = 1 + iota
	pathEsc
	pathQuery
	pathOpen
)

// pathClass marks the bytes getComp stops at: separators, escapes and the
// bytes that give a gjson path a meaning beyond plain keys and indexes.
// '[' and '{' only matter at the start of a component after a dot, where
// they start a multipath.
var pathClass = [256]uint8{'.': pathDot, '\\': pathEsc, '|': pathQuery, '#': pathQuery, '*': pathQuery, '?': pathQuery, '@': pathQuery, '[': pathOpen, '{': pathOpen}

// getComp is nextComp for Get paths: it reads the component that starts at
// i, whose bytes before j the caller knows to have no path class (j is i
// when it does not). next is -2 when the component holds a byte that makes
// the path a query, or starts a multipath after a dot; Get then leaves the
// path to the full path engine. Checking here, one component at a time,
// saves a separate pass over the path.
func getComp(path string, i, j int) (raw string, next int, esc bool) {
	for ; j < len(path); j++ {
		c := pathClass[path[j]]
		if c == 0 {
			continue // most bytes: one test, no search over the classes
		}
		switch c {
		case pathDot:
			return path[i:j], j + 1, esc
		case pathEsc:
			esc = true
			j++
		case pathOpen:
			if j == i && i > 0 {
				return "", -2, false // a multipath after a dot
			}
		default:
			return "", -2, false
		}
	}
	return path[i:], -1, esc
}

// queryStart reports whether path starts like a multipath, a literal or a
// JSON lines query, which plain component walks do not handle.
func queryStart(path string) bool {
	switch path[0] {
	case '[', '{', '!':
		return true
	case '.':
		return len(path) > 1 && path[1] == '.'
	}
	return false
}

// unescapePath removes path escapes from a raw component.
func unescapePath(raw string) string {
	b := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' {
			i++
			if i == len(raw) {
				break
			}
		}
		b = append(b, raw[i])
	}
	return string(b)
}

// compKey returns the unescaped component.
func compKey(raw string, esc bool) string {
	if esc {
		return unescapePath(raw)
	}
	return raw
}

// plainComps reports whether the components of a path that queryStart
// does not match are all plain keys and indexes. A '[' or '{' after a dot
// starts a multipath, which applies to the value before the dot.
func plainComps(path string) bool {
	dot := -1 // the last unescaped dot
	for i := 0; i < len(path); i++ {
		c := pathClass[path[i]]
		if c == 0 {
			continue // most bytes: one test
		}
		if c == pathEsc {
			i++
		} else if c == pathDot {
			dot = i
		} else if c == pathQuery || dot == i-1 {
			return false // a query byte, or '[' or '{' after a dot
		}
	}
	return true
}

// isDigits reports whether s is non-empty and only ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// arrayIndex parses an all-digit component the way gjson does: uint64
// arithmetic that wraps on overflow. Indexes beyond int range cannot match.
func arrayIndex(s string) (int, bool) {
	if !isDigits(s) {
		return 0, false
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		n = n*10 + uint64(s[i]-'0')
	}
	if n > uint64(maxInt) {
		return 0, false
	}
	return int(n), true
}

const maxInt = int(^uint(0) >> 1)

// setComp is a parsed component of an sjson path. It is kept to 24 bytes:
// every edit zeroes an array of them and copies them as it walks.
type setComp struct {
	key   string // escapes removed, except in a lookup component
	force bool   // ':' prefix: always an object key
	esc   bool   // has a backslash: never an existing array element
	look  bool   // lookupComp: key is the component as gjson reads it
}

// parseSetPath splits an sjson path into components and classifies it in
// the same pass, which every edit makes. kind is setComplex for a query
// path, one with an unescaped '|', '#', '*', '?' or '@', whose components it
// does not finish; setLookup when a component is a lookupComp; and
// setPlain otherwise. sjson treats a leading unescaped ':' as "this is an
// object key even if it looks numeric".
func parseSetPath(path string, buf []setComp) (comps []setComp, kind int) {
	comps, kind = buf[:0], setPlain
	unesc := false // a component other than a lookup has escapes
	for i := 0; ; {
		j, esc := i, false
		for ; j < len(path); j++ {
			c := setClass[path[j]]
			if c == 0 {
				continue
			}
			if c == pathDot {
				break
			}
			if c == pathQuery {
				return comps, setComplex
			}
			esc = true
			j++ // the escaped byte; a trailing lone backslash is dropped
		}
		c := setComp{key: path[i:min(j, len(path))], esc: esc}
		if c.key != "" && c.key[0] == ':' {
			c.force, c.key = true, c.key[1:]
		}
		if lookupComp(c.key) {
			c.look, kind = true, setLookup
		} else if esc {
			unesc = true
		}
		comps = append(comps, c)
		if j >= len(path) {
			break
		}
		i = j + 1
	}
	// Escapes go once the path is known not to be a query.
	if unesc {
		for k := range comps {
			if c := &comps[k]; c.esc && !c.look {
				c.key = unescapePath(c.key)
			}
		}
	}
	return comps, kind
}

// name returns the key c names without escapes. A lookup component keeps
// the text gjson reads in key, and is unescaped here when the key is
// needed.
func (c *setComp) name() string {
	if c.look && c.esc {
		return unescapePath(c.key)
	}
	return c.key
}

// lookupComp reports whether sjson finds the member a component names by
// gjson's reading of it, which differs from the plain key: '[' and '{' start
// a multipath, '!' a literal. Such a component can name a member under
// another key, or none.
func lookupComp(raw string) bool {
	return raw != "" && (raw[0] == '[' || raw[0] == '{' || raw[0] == '!')
}

// Kinds of sjson paths, from parseSetPath.
const (
	setPlain   = iota
	setLookup  // a component is a lookupComp
	setComplex // a query path
)

// setClass marks the bytes parseSetPath stops at: separators, escapes and
// the query bytes of sjson.
var setClass = [256]uint8{'.': pathDot, '\\': pathEsc, '|': pathQuery, '#': pathQuery, '*': pathQuery, '?': pathQuery, '@': pathQuery}

// quoteKeyPath reports whether the last component of path is a key that
// starts with a quote; sjson deletes such members as quoteSplice describes.
func quoteKeyPath(path string) bool {
	start := 0
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '\\':
			i++
		case '.':
			start = i + 1
		}
	}
	raw := path[start:]
	if raw != "" && raw[0] == ':' {
		raw = raw[1:]
	}
	return raw != "" && (raw[0] == '"' || len(raw) > 1 && raw[0] == '\\' && raw[1] == '"')
}

// findIndex is setIndex for finding an existing element: sjson never finds
// one by an escaped number, and pads the array from its end instead.
func (c setComp) findIndex() (idx, kind int) {
	idx, kind = c.setIndex()
	if c.esc && kind == idxNumber {
		kind = idxPad
	}
	return idx, kind
}

// setIndex reports how an sjson component addresses an array element:
// "-1" appends, "" appends after compacting the array, and digits name an
// index. A ':' prefix only keeps a component from creating elements; an
// existing element is still found by its number.
func (c setComp) setIndex() (idx, kind int) {
	if !isDigits(c.key) {
		switch {
		case c.force:
		case c.key == "-1":
			return 0, idxAppend
		case c.key == "":
			return 0, idxEmpty
		}
		return 0, idxNone
	}
	n := 0
	for i := 0; i < len(c.key); i++ {
		if n > (maxInt-9)/10 {
			n = maxInt // longer than any array that fits in memory
			break
		}
		n = n*10 + int(c.key[i]-'0')
	}
	if c.force {
		return n, idxForced
	}
	return n, idxNumber
}

const (
	idxNone = iota
	idxNumber
	idxForced
	idxAppend
	idxEmpty
	idxPad // an escaped number: added after the last element, padded up to it
)

// maxPad bounds the nulls a write may add to reach an index. sjson pads
// arrays up to any index, so a path such as "a.99999999999" makes it
// allocate until the process runs out of memory. ojson reports an error.
const maxPad = 1 << 24

func nonNumericErr(key string) error {
	return &pathError{"cannot set array element for non-numeric key '" + key + "'"}
}

type pathError struct{ msg string }

func (e *pathError) Error() string { return e.msg }

var (
	errEmptyPath   = &pathError{"path cannot be empty"}
	errComplexDel  = &pathError{"cannot delete value from a complex path"}
	errHugeIndex   = &pathError{"ojson: array index too large"}
	errUnsupported = &pathError{"ojson: query paths are not supported yet"}
)

// plainWord reports whether the eight bytes of w all lie in ']'..'z': the
// lowercase letters, '_' and four other bytes, none of which has a path
// class. Long keys are mostly such bytes, which a component read can then
// pass eight at a time.
func plainWord(w uint64) bool {
	a := w &^ msb
	return (^(a+lsb*(0x80-']'))|(a+lsb*(0x7f-'z'))|w)&msb == 0
}
