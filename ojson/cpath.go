package ojson

// Syntax of gjson paths beyond plain keys and indexes. Components are read
// as the evaluator reaches them, because the same text means different
// things against an object and against an array.

// objComp is a path component read against an object.
type objComp struct {
	key   string // escapes removed
	wild  bool   // key holds '*' or '?'
	more  bool   // rest follows a '.'
	rest  string
	piped bool // pipe follows a '|' or a '.' that starts a pipe
	pipe  string
}

// readObjComp reads the component that path starts with.
func readObjComp(path string) (c objComp) {
	var buf []byte // the key without escapes, once an escape is seen
	for i := 0; i < len(path); i++ {
		ch := path[i]
		switch ch {
		case '|':
			c.key = escKey(path[:i], buf)
			c.piped, c.pipe = true, path[i+1:]
			return c
		case '.':
			c.key = escKey(path[:i], buf)
			if i+1 < len(path) && dotPipe(path[i+1:]) {
				c.piped, c.pipe = true, path[i+1:]
			} else {
				c.more, c.rest = true, path[i+1:]
			}
			return c
		case '*', '?':
			c.wild = true
		case '\\':
			if buf == nil {
				buf = append(make([]byte, 0, len(path)), path[:i]...)
			}
			if i++; i < len(path) {
				buf = append(buf, path[i])
			}
			continue
		}
		if buf != nil {
			buf = append(buf, ch)
		}
	}
	c.key = escKey(path, buf)
	return c
}

func escKey(raw string, buf []byte) string {
	if buf != nil {
		return string(buf)
	}
	return raw
}

// dotPipe reports whether the text after a '.' starts a pipe: a multipath
// or a registered modifier.
func dotPipe(s string) bool {
	if DisableModifiers {
		return false
	}
	switch s[0] {
	case '[', '{':
		return true
	case '@':
		i := 1
		for i < len(s) && s[i] != '.' && s[i] != '|' && s[i] != ':' {
			i++
		}
		return modifierKnown(s[1:i])
	}
	return false
}

// query is a "#(...)" or "#(...)#" array query.
type query struct {
	on, all bool
	path    string
	op      string
	val     string
}

// arrComp is a path component read against an array.
type arrComp struct {
	part    string
	hash    bool // part holds '#': a count, a '#.' map or a query
	alog    bool // "#." maps alogKey over the elements
	alogKey string
	q       query
	more    bool
	rest    string
	piped   bool
	pipe    string
}

// readArrComp reads the component that path starts with.
func readArrComp(path string) (c arrComp) {
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '|':
			c.part = path[:i]
			c.piped, c.pipe = true, path[i+1:]
			return c
		case '.':
			c.part = path[:i]
			if !c.hash && i+1 < len(path) && dotPipe(path[i+1:]) {
				c.piped, c.pipe = true, path[i+1:]
			} else {
				c.more, c.rest = true, path[i+1:]
			}
			return c
		case '#':
			c.hash = true
			if i != 0 || len(path) == 1 {
				continue
			}
			switch path[1] {
			case '.':
				c.alog, c.alogKey = true, path[2:]
			case '(', '[':
				c.q.on = true
				end, ok := c.q.parse(path)
				if !ok {
					c.part = path
					return c
				}
				if end < len(path) && path[end] == '#' {
					c.q.all = true
				}
				i = end - 1
			}
		}
	}
	c.part = path
	return c
}

// parse reads the query that path starts with, "#(" or "#[", up to its
// closing bracket, and returns the index after that bracket.
func (q *query) parse(path string) (int, bool) {
	depth, opAt, vesc := 1, 0, false
	i := 2
scan:
	for ; i < len(path); i++ {
		ch := path[i]
		if depth == 1 && opAt == 0 {
			switch ch {
			case '!', '=', '<', '>', '%':
				opAt = i
				continue
			}
		}
		switch ch {
		case '\\':
			i++
		case '(', '[':
			depth++
		case ')', ']':
			if depth--; depth == 0 {
				break scan
			}
		case '"':
			for i++; i < len(path); i++ {
				if path[i] == '\\' {
					vesc = true
					i++
				} else if path[i] == '"' {
					break
				}
			}
		}
	}
	if depth > 0 {
		return 0, false
	}
	if opAt == 0 {
		q.path = trimText(path[2:i])
		return i + 1, true
	}
	q.path = trimText(path[2:opAt])
	v := trimText(path[opAt:i])
	n := 1
	switch {
	case len(v) == 1:
	case v[1] == '=' && (v[0] == '!' || v[0] == '<' || v[0] == '>'), v[0] == '!' && v[1] == '%':
		n = 2
	case v[0] == '=' && v[1] == '=':
		v = v[1:]
	case v[0] == '!':
		n = 0
	}
	q.op, v = v[:n], trimText(v[n:])
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
		if vesc {
			// Decoding stops at a malformed escape or a raw control byte.
			for k := 0; k < len(v); k++ {
				if v[k] < ' ' {
					v = v[:k]
					break
				}
			}
			b, _ := unescape(nil, v)
			v = string(b)
		}
	}
	q.val = v
	return i + 1, true
}

// trimText trims bytes up to ' ' from both ends of s.
func trimText(s string) string {
	for len(s) > 0 && s[0] <= ' ' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] <= ' ' {
		s = s[:len(s)-1]
	}
	return s
}

// pipeSplit splits the rest of a path after "#." or a query at the pipe
// that applies to the collected result, when there is one. A '|' inside a
// nested query, or after a leading multipath, does not split it.
func pipeSplit(r string) (left, right string, ok bool) {
	if r == "" {
		return "", "", false
	}
	if r[0] == '{' {
		j := 1
		if len(r) > 1 && r[1] == '"' {
			if j = quotedEnd(r, 1); j < 0 {
				return "", "", false
			}
		} else if j = balancedEnd(r, 2, 1); j < 0 {
			return "", "", false
		}
		if j < len(r) && r[j] == '|' {
			return r[:j], r[j+1:], true
		}
		return "", "", false
	}
	for i := 0; i < len(r); i++ {
		switch r[i] {
		case '\\':
			i++
		case '|':
			return r[:i], r[i+1:], true
		case '.':
			if i+1 >= len(r) {
				return "", "", false
			}
			if r[i+1] != '#' {
				continue
			}
			// "#" and the byte after it are skipped, and a query after
			// it up to its closing bracket.
			if i += 2; i == len(r) {
				return "", "", false
			}
			if r[i] == '(' || r[i] == '[' {
				i = selectorEnd(r, i)
			}
		}
	}
	return "", "", false
}

// selectorEnd returns the index of the bracket that closes the one at s[i],
// counting only brackets of its kind and skipping escapes and strings, or
// len(s).
func selectorEnd(s string, i int) int {
	open, close := s[i], byte(')')
	if open == '[' {
		close = ']'
	}
	depth := 1
	for i++; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case open:
			depth++
		case close:
			if depth--; depth == 0 {
				return i
			}
		case '"':
			for i++; i < len(s); i++ {
				if s[i] == '\\' {
					i++
				} else if s[i] == '"' {
					break
				}
			}
		}
	}
	return len(s)
}

// quotedEnd returns the index after the string that starts at s[i], or -1.
func quotedEnd(s string, i int) int {
	for i++; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return -1
}

// balancedEnd returns the index after the bracket that brings depth to 0,
// scanning from s[i] and skipping strings, or -1.
func balancedEnd(s string, i, depth int) int {
	for ; i < len(s); i++ {
		switch s[i] {
		case '"':
			if i = quotedEnd(s, i); i < 0 {
				return -1
			}
			i--
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth--; depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// subSel is one component of a multipath: "name:path" or just "path".
type subSel struct{ name, path string }

// readSubs appends to sels the components of the multipath that path
// starts with, "[...]" or "{...}", and returns the path after its closing
// bracket.
func readSubs(path string, sels []subSel) (_ []subSel, rest string, ok bool) {
	mod, colon, start, depth := 0, 0, 1, 1
	for i := 1; i < len(path); i++ {
		end := false
		switch path[i] {
		case '\\':
			i++
		case '@':
			if mod == 0 && (path[i-1] == '.' || path[i-1] == '|') {
				mod = i
			}
		case ':':
			if mod == 0 && colon == 0 && depth == 1 {
				colon = i
			}
		case ',':
			end = depth == 1
		case '"':
			for i++; i < len(path); i++ {
				if path[i] == '\\' {
					i++
				} else if path[i] == '"' {
					break
				}
			}
		case '[', '(', '{':
			depth++
		case ']', ')', '}':
			depth--
			end = depth == 0
		}
		if !end {
			continue
		}
		if colon == 0 {
			sels = append(sels, subSel{path: path[start:i]})
		} else {
			sels = append(sels, subSel{name: path[start:colon], path: path[colon+1 : i]})
		}
		if depth == 0 {
			return sels, path[i+1:], true
		}
		mod, colon, start = 0, 0, i+1
	}
	return nil, "", false
}

// lastComp returns the last component of path, after the last unescaped
// '.' or '|'.
func lastComp(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if (path[i] == '|' || path[i] == '.') && (i == 0 || path[i-1] != '\\') {
			return path[i+1:]
		}
	}
	return path
}

// simpleName reports whether a multipath component can name its value in
// an object.
func simpleName(s string) bool {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c < ' ':
			return false
		case c == '[', c == ']', c == '{', c == '}', c == '(', c == ')', c == '#', c == '|', c == '!':
			return false
		}
	}
	return true
}
