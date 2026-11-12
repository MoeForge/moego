package ojson

import (
	"strings"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// Wildcard components whose only wildcard is a final '*' after ASCII bytes,
// such as "tr*", match the keys that start with those bytes. find reads
// them with a member loop of its own instead of the general one of
// findWild, which decodes each key and runs globMatch on it.

// findPrefix is findWild for the component path[pi:e+1], where path[e] is
// the '*' wildScan found: the members whose keys start with path[pi:e] are
// tried in order until the rest of the path resolves.
func findPrefix(json string, i int, path string, pi, e int) (vs, ve int, esc bool) {
	if json[i] != '{' {
		return -1, -1, false
	}
	next := -1
	if e+1 < len(path) {
		next = e + 2
	}
	prefix := path[pi:e]
	pw, pm := prefixWord(prefix)
	for i++; ; {
		v := memberPrefix(json, i, prefix, pw, pm)
		if v < 0 {
			return -1, -1, false
		}
		if next < 0 {
			ve, esc := valueSpan(json, v)
			return v, ve, esc
		}
		if c := json[v]; c == '{' || c == '[' {
			if fs, fe, fesc := find(json, v, path, next, 0); fs != -1 {
				return fs, fe, fesc
			}
		}
		if i = skipValue(json, v); i < 0 {
			return -1, -1, false
		}
	}
}

// prefixWord returns prefix as a little-endian word and the mask of its
// bytes, when it has one to eight bytes and no quote. A member key starts
// with prefix when its first bytes, compared under the mask, equal the
// word: a shorter key differs at its closing quote. pm is 0 otherwise.
func prefixWord(prefix string) (pw, pm uint64) {
	n := len(prefix)
	if n == 0 || n > 8 || strings.IndexByte(prefix, '"') >= 0 {
		return 0, 0
	}
	for j := n - 1; j >= 0; j-- {
		pw = pw<<8 | uint64(prefix[j])
	}
	return pw, ^uint64(0) >> (64 - 8*uint(n))
}

// memberPrefix is memberValue for the first member at or after json[i]
// whose key starts with prefix. pw and pm are prefixWord(prefix).
func memberPrefix(json string, i int, prefix string, pw, pm uint64) int {
	for {
		if i >= len(json) || json[i] != '"' {
			i = skipSpace(json, i)
			if i >= len(json) || json[i] != '"' {
				return -1
			}
		}
		ks := i + 1
		var match bool
		if i = quickEnd(json, ks); i > 0 {
			if pm != 0 {
				match = (jsonsimd.Load64(json, ks)^pw)&pm == 0
			} else {
				match = strings.HasPrefix(json[ks:i-1], prefix)
			}
		} else {
			var e bool
			if i, e = scanString(json, ks); i < 0 {
				return -1
			}
			match = keyHasPrefix(json[ks:i-1], e, prefix)
		}
		if i >= len(json) || json[i] != ':' {
			i = skipSpace(json, i)
			if i >= len(json) || json[i] != ':' {
				return -1
			}
		}
		i++
		if i >= len(json) || json[i] <= ' ' {
			i = skipSpace(json, i)
			if i >= len(json) {
				return -1
			}
		}
		if match {
			return i
		}
		if i = skipValue(json, i); i < 0 {
			return -1
		}
	}
}

// keyHasPrefix reports whether the member key raw, without quotes, starts
// with prefix once decoded, as keyMatch decodes it.
func keyHasPrefix(raw string, esc bool, prefix string) bool {
	if !esc {
		return strings.HasPrefix(raw, prefix)
	}
	var buf [64]byte
	b, _ := unescape(buf[:0], raw)
	return strings.HasPrefix(bytesView(b), prefix)
}
