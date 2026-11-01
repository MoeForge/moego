package ojson

import "strings"

// Set on a path that sjson treats as a query: the path is read as Get reads
// it, and every value found is replaced where Index or Indexes place it.
// Unlike sjson, a value is replaced only when that place holds exactly that
// value in the input; otherwise the document is left as it is, so that a
// valid document never becomes invalid. sjson writes over whatever bytes the
// offsets point to, including offsets of modifier output or of counts.

// errQuery is planSet's error for a query path: the callers use setQuery.
var errQuery = &pathError{"ojson: query path"}

// setQuery returns json with the values path names replaced by v.
func setQuery(json, path string, v piece) []byte {
	var buf [8]int
	at, ok := queryTargets(json, path, buf[:0])
	if !ok {
		b := makeNoZero(len(json))
		copy(b, json)
		return b
	}
	return spliceAll(json, at, v, queryLen(json, at, v))
}

// setQueryBytes is setQuery for SetBytesOptions and SetRawBytesOptions:
// with ReplaceInPlace the result is written into json's memory when it fits.
func setQueryBytes(json []byte, path string, v piece, opts *Options) []byte {
	s := bytesView(json)
	if !inPlace(opts) {
		return setQuery(s, path, v)
	}
	var buf [8]int
	at, ok := queryTargets(s, path, buf[:0])
	if !ok {
		return json
	}
	n := queryLen(s, at, v)
	if n > cap(json) || insertsFrom(&v, json[:cap(json)]) {
		return spliceAll(s, at, v, n)
	}
	return spliceInPlace(json, at, v, n)
}

// queryLen returns the length of json with the spans at replaced by v.
func queryLen(json string, at []int, v piece) int {
	n, vl := len(json), v.len()
	for i := 0; i < len(at); i += 2 {
		n += vl - (at[i+1] - at[i])
	}
	return n
}

// spliceAll returns json with the spans at replaced by v, n bytes long.
func spliceAll(json string, at []int, v piece, n int) []byte {
	dst := makeNoZero(n)[:0]
	prev := 0
	for i := 0; i < len(at); i += 2 {
		dst = append(dst, json[prev:at[i]]...)
		dst = v.appendTo(dst)
		prev = at[i+1]
	}
	return append(dst, json[prev:]...)
}

// spliceInPlace replaces the spans at of json by v inside json's memory,
// whose capacity holds the n bytes of the result. The text between spans
// moves by d, the sum of the length changes before it: the parts that move
// left go first, from left to right, then the parts that move right, from
// right to left, so that no part is overwritten before it moves. v goes in
// last.
func spliceInPlace(json []byte, at []int, v piece, n int) []byte {
	buf := json[:cap(json)]
	vl := v.len()
	end := func(j int) int {
		if j+2 < len(at) {
			return at[j+2]
		}
		return len(json)
	}
	d := 0
	for j := 0; j < len(at); j += 2 {
		d += vl - (at[j+1] - at[j])
		if d < 0 {
			copy(buf[at[j+1]+d:], buf[at[j+1]:end(j)])
		}
	}
	for j := len(at) - 2; j >= 0; j -= 2 {
		if d > 0 {
			copy(buf[at[j+1]+d:], buf[at[j+1]:end(j)])
		}
		d -= vl - (at[j+1] - at[j])
		v.appendTo(buf[at[j]+d : at[j]+d])
	}
	return buf[:n]
}

// insertsFrom reports whether any part of v lies in mem.
func insertsFrom(v *piece, mem []byte) bool {
	m := bytesView(mem)
	for _, s := range v {
		if within(s, m) {
			return true
		}
	}
	return false
}

// queryTargets appends to at the spans that path sets in json, as lo, hi
// pairs in increasing order. It reports false when there are none, or when
// any of them is not a value of the input.
func queryTargets(json, path string, at []int) ([]int, bool) {
	r := Get(json, path)
	switch {
	case r.Indexes != nil:
		// Pair the elements of r.Raw with r.Indexes.
		s := r.Raw
		i := skipSpace(s, 0)
		if i >= len(s) || s[i] != '[' {
			return at, false
		}
		k := 0
		for i++; ; k++ {
			for i < len(s) && (s[i] <= ' ' || s[i] == ',') {
				i++
			}
			if i >= len(s) || s[i] == ']' {
				break
			}
			ve, _ := valueSpan(s, i)
			if ve <= i || k >= len(r.Indexes) {
				return at, false
			}
			at = addTarget(at, json, r.Indexes[k], s[i:ve])
			i = ve
		}
		if k != len(r.Indexes) {
			return at, false
		}
	case r.Index > 0 && r.Raw != "":
		at = addTarget(at, json, r.Index, r.Raw)
	default:
		return at, false
	}
	if len(at) == 0 {
		return at, false
	}
	for i := 0; i < len(at); i += 2 {
		if at[i] < 0 {
			return at, false
		}
		if i > 0 && at[i] < at[i-1] {
			return at, false // overlapping or repeated
		}
	}
	return at, outsideStrings(json, at)
}

// outsideStrings reports whether no span of at starts inside the text of a
// string of json. A path that reads into the text of a string finds there
// what looks like a value, such as the 2 of "[1,2]".
func outsideStrings(json string, at []int) bool {
	i := 0
	for k := 0; k < len(at); k += 2 {
		lo := at[k]
		for {
			q := strings.IndexByte(json[i:lo], '"')
			if q < 0 {
				break
			}
			if i = stringEnd(json, i+q+1); i < 0 || i > lo {
				return false
			}
		}
		i = at[k+1]
	}
	return true
}

// addTarget appends the span of raw at json[lo], or a negative pair when
// json does not hold raw there as one value, keeping the pairs sorted.
func addTarget(at []int, json string, lo int, raw string) []int {
	hi := lo + len(raw)
	if !valueAtSpan(json, lo, hi, raw) {
		lo, hi = -1, -1
	}
	j := len(at)
	at = append(at, lo, hi)
	for ; j > 0 && at[j-2] > lo; j -= 2 {
		at[j], at[j+1], at[j-2], at[j-1] = at[j-2], at[j-1], lo, hi
	}
	return at
}

// valueAtSpan reports whether json[lo:hi] is raw and holds one whole value,
// possibly followed by whitespace, in a place where a value stands.
func valueAtSpan(json string, lo, hi int, raw string) bool {
	if lo < 0 || hi > len(json) || json[lo:hi] != raw {
		return false
	}
	ve, _ := valueSpan(json, lo)
	if ve <= lo || ve > hi {
		return false
	}
	for i := ve; i < hi; i++ {
		if !isSpace(json[i]) {
			return false
		}
	}
	p := lo - 1
	for p >= 0 && isSpace(json[p]) {
		p--
	}
	n := skipSpace(json, hi)
	if p < 0 {
		return n == len(json) // the root value
	}
	switch json[p] {
	case ':', ',', '[':
	default:
		return false
	}
	return n < len(json) && (json[n] == ',' || json[n] == ']' || json[n] == '}')
}
