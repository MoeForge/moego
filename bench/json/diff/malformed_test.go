package diff

import (
	"strings"

	"github.com/tidwall/gjson"
)

// When a path makes gjson parse text that is not valid JSON, the case is
// malformed input, even though the document is valid, and Get is only
// checked for crashes. A fresh parse looks for the first '{' or '[' in the
// text it is given, so a path that goes on into a string value reads the
// string's characters as JSON; modifier output, literals and modifier
// arguments are parsed as well. Wherever that text is valid JSON the
// comparison stays exact.

// malformedArg reports a modifier whose JSON argument ('{', '[' or '"')
// is cut short or not valid JSON. An argument that starts right after the
// ':' is one JSON value; after leading whitespace the argument is the rest
// of the path up to a '|', and the modifier parses all of it
// (`@join: {}."preserve"1}`).
func malformedArg(p string) bool {
	for _, m := range reMod.FindAllStringSubmatchIndex(p, -1) {
		i := m[3] // after the name
		if !isMod(p[m[2]:m[3]]) || i >= len(p) || p[i] != ':' {
			continue
		}
		for i++; i < len(p) && p[i] <= ' '; i++ {
		}
		if i >= len(p) || p[i] != '{' && p[i] != '[' && p[i] != '"' {
			continue
		}
		if i > m[3]+1 {
			end := strings.IndexByte(p[i:], '|')
			if end < 0 {
				end = len(p) - i
			}
			if !gjson.Valid(p[i : i+end]) {
				return true
			}
		} else if badJSONAt(p, i) {
			return true
		}
	}
	return false
}

// malformedParse reports a case whose Path or Sub, or whose Sub applied to
// Path's result (Result.Get), makes gjson parse malformed text.
func malformedParse(c gcase) bool {
	bad := false
	catch(false, func() {
		r := gjson.Get(c.Doc, c.Path)
		// A result whose Raw is not valid JSON is handled where it is
		// parsed again: its methods and Result.Get are compared for
		// crashes only (runOp get and rget).
		bad = parsesBad(c.Doc, c.Path) || parsesBad(c.Doc, c.Sub) || parsesBad(r.Raw, c.Sub)
	})
	return bad
}

// badString reports a JSON string whose text, from its first '{' or '['
// on, does not start with a valid JSON container: what a fresh parse of
// the string reads.
func badString(t string) bool {
	t = strings.Trim(t, " \t\r\n")
	if len(t) < 2 || t[0] != '"' {
		return false
	}
	in := t[1 : len(t)-1]
	i := strings.IndexAny(in, "{[")
	if i < 0 {
		return false
	}
	return badJSONAt(in, i)
}

// comp is one top-level path component and the separator after it ('.',
// '|', or 0 at the end).
type comp struct {
	s, e int
	sep  byte
}

// topComps splits p at '.' and '|' outside queries `#(...)`, multipaths
// and modifier arguments, escapes and quoted text (quotes count inside
// brackets, and right after '!' or ':'). A '(' opens only in a component
// that starts with '#', and '[' or '{' only at the start of a component,
// after a component's leading '!' or after ':'; elsewhere they are part
// of a key.
func topComps(p string) []comp {
	var cs []comp
	depth, last := 0, 0
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case c == '\\':
			i++
		case c == '"' && (depth > 0 || i > 0 && (p[i-1] == '!' || p[i-1] == ':')):
			for i++; i < len(p) && p[i] != '"'; i++ {
				if p[i] == '\\' {
					i++
				}
			}
		case depth > 0 && (c == '(' || c == '[' || c == '{'),
			c == '(' && i == last+1 && p[last] == '#',
			(c == '[' || c == '{') && (i == last || p[i-1] == ':' || i == last+1 && p[last] == '!'):
			depth++
		case (c == ')' || c == ']' || c == '}') && depth > 0:
			depth--
		case depth == 0 && (c == '.' || c == '|'):
			cs = append(cs, comp{last, i, c})
			last = i + 1
		}
	}
	return append(cs, comp{last, len(p), 0})
}

// perElem is the part of path after component k that a `#.` map
// evaluates on each element: up to the first pipe.
func perElem(path string, cs []comp, k int) string {
	end := len(path)
	for _, c := range cs[k+1:] {
		if c.sep == '|' {
			end = c.e
			break
		}
	}
	return path[cs[k].e+1 : end]
}

// topColon returns the index of the first ':' of s outside brackets and
// quotes, or -1.
func topColon(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\':
			i++
		case c == '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' {
					i++
				}
			}
		case c == '(' || c == '[' || c == '{':
			depth++
		case (c == ')' || c == ']' || c == '}') && depth > 0:
			depth--
		case depth == 0 && c == ':':
			return i
		}
	}
	return -1
}

// splitTop splits the inside of a multipath at commas outside brackets
// and quotes.
func splitTop(s string) []string {
	var out []string
	depth, last := 0, 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\':
			i++
		case c == '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' {
					i++
				}
			}
		case c == '(' || c == '[' || c == '{':
			depth++
		case (c == ')' || c == ']' || c == '}') && depth > 0:
			depth--
		case depth == 0 && c == ',':
			out = append(out, s[last:i])
			last = i + 1
		}
	}
	return append(out, s[last:])
}

// parsesBad reports whether evaluating path on json makes gjson parse
// malformed text: json itself is a string with bad bracketed text and the
// path reads into it, or the text a later step is evaluated on (after a
// pipe, a modifier, a literal or a query; each element of a `#.` or
// `#(...)#` map; a multipath's members) is malformed. Over-approximates
// a little: the case then is only checked for crashes.
func parsesBad(json, path string) bool {
	if path == "" {
		return false
	}
	cs := topComps(path)
	at := func(k int) string { // the text component k is evaluated on
		if k == 0 {
			return json
		}
		return gjson.Get(json, path[:cs[k-1].e]).Raw
	}
	// scans reports a component whose fresh parse looks for the first
	// '{' or '[' of its text: a key, index, wildcard or query. A
	// modifier or literal takes a string as a value; a multipath's
	// members are checked one by one.
	scans := func(k int) bool {
		if k >= len(cs) {
			return false
		}
		n := path[cs[k].s:cs[k].e]
		return n != "" && !strings.ContainsAny(n[:1], "@![{")
	}
	// bad reports malformed text t for component k to be evaluated on.
	bad := func(t string, k int) bool {
		return strings.TrimSpace(t) != "" && !gjson.Valid(t) || scans(k) && badString(t)
	}
	for k, c := range cs {
		cp := path[c.s:c.e]
		if cp == "" {
			continue // the ".." of JSON Lines, or an empty key
		}
		if k == 0 && scans(0) && badString(json) {
			return true
		}
		switch cp[0] {
		case '[', '{':
			if in := at(k); len(cp) >= 2 {
				for _, e := range splitTop(cp[1 : len(cp)-1]) {
					// A member may carry a name (`"n":path`, `n:path`,
					// `:path`); its path is checked with and without
					// the part up to the first top-level ':'.
					e = strings.TrimSpace(e)
					if parsesBad(in, e) {
						return true
					}
					if j := topColon(e); j >= 0 && parsesBad(in, e[j+1:]) {
						return true
					}
				}
			}
		case '#':
			switch {
			case cp == "#" && c.sep == '.':
				// `#.` evaluates the rest, up to a pipe, on each element.
				if a := gjson.Parse(at(k)); a.IsArray() {
					for _, e := range a.Array() {
						if parsesBad(e.Raw, perElem(path, cs, k)) {
							return true
						}
					}
				}
			case len(cp) > 2 && cp[1] == '(' && !strings.ContainsRune("=!<>%)", rune(cp[2])):
				// The condition's path is evaluated on each element.
				for _, e := range gjson.Parse(at(k)).Array() {
					if badString(e.Raw) {
						return true
					}
				}
			}
			if strings.HasSuffix(cp, ")#") && c.sep == '.' {
				for _, e := range gjson.Get(json, path[:c.e]).Array() {
					if parsesBad(e.Raw, perElem(path, cs, k)) {
						return true
					}
				}
			}
		}
		if cp[0] == '@' {
			// The rest of the path (and Parse, at the end) reads the
			// modifier's whole output, which `|@tostr` returns as a
			// string; the Result holds only its first value.
			if out := gjson.Get(json, path[:c.e]+"|@tostr"); out.Type == gjson.String && bad(out.Str, k+1) {
				return true
			}
		}
		if c.sep == 0 {
			break
		}
		// A pipe, or a modifier, literal or query result, is parsed
		// afresh by the next step; a plain key's value is not.
		if c.sep == '|' || strings.ContainsAny(cp[:1], "@!#[{") {
			if r := gjson.Get(json, path[:c.e]); r.Exists() && bad(r.Raw, k+1) {
				return true
			}
		}
	}
	return false
}
