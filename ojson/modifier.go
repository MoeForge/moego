package ojson

import (
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// DisableModifiers turns off modifiers: an '@name' path component is then
// read as a plain key.
var DisableModifiers = false

// customMods holds the modifiers added with AddModifier. It is replaced as a
// whole on every addition, so lookups need no lock.
var (
	customMods atomic.Pointer[map[string]func(json, arg string) string]
	modMu      sync.Mutex
)

// AddModifier binds a custom modifier command to the path syntax: '@name'
// or '@name:arg' calls fn with the JSON of the current value and the
// argument text. A custom modifier replaces a built-in one with the same
// name. Unlike in gjson, AddModifier is safe to call while other goroutines
// read.
func AddModifier(name string, fn func(json, arg string) string) {
	modMu.Lock()
	defer modMu.Unlock()
	old := customMods.Load()
	m := make(map[string]func(json, arg string) string, 1)
	if old != nil {
		for k, v := range *old {
			m[k] = v
		}
	}
	m[name] = fn
	customMods.Store(&m)
}

// ModifierExists reports whether a modifier with the name exists, built in
// or added with AddModifier. fn is not used.
func ModifierExists(name string, fn func(json, arg string) string) bool {
	return customMod(name) != nil || builtinMod(name)
}

func customMod(name string) func(json, arg string) string {
	if m := customMods.Load(); m != nil {
		return (*m)[name]
	}
	return nil
}

// builtinMod reports whether name is a built-in modifier.
func builtinMod(name string) bool {
	switch name {
	case "reverse", "ugly", "pretty", "this", "valid", "flatten", "join",
		"keys", "values", "tostr", "fromstr", "group", "dig":
		return true
	}
	return false
}

// modifierKnown reports whether '@name' is a modifier: modifiers are enabled
// and one with the name exists.
func modifierKnown(name string) bool {
	return !DisableModifiers && ModifierExists(name, nil)
}

// applyModifier runs the modifier name on json with arg. It reports false
// when modifiers are disabled or there is no such modifier.
func applyModifier(name, json, arg string) (string, bool) {
	if DisableModifiers {
		return "", false
	}
	if fn := customMod(name); fn != nil {
		return fn(json, arg), true
	}
	switch name {
	case "this":
		return json, true
	case "reverse":
		return modReverse(json), true
	case "ugly":
		return modUgly(json), true
	case "pretty":
		return modPretty(json, arg), true
	case "valid":
		if !Valid(json) {
			return "", true
		}
		return json, true
	case "flatten":
		return modFlatten(json, arg), true
	case "join":
		return modJoin(json, arg), true
	case "keys":
		return modKeys(json), true
	case "values":
		return modValues(json), true
	case "tostr":
		return bytesView(appendJSONString(nil, json, !DisableEscapeHTML)), true
	case "fromstr":
		if !Valid(json) {
			return "", true
		}
		return Parse(json).String(), true
	case "group":
		return modGroup(json), true
	case "dig":
		return modDig(json, arg), true
	}
	return "", false
}

// outLimit bounds the output of the modifiers whose output can grow faster
// than their input: @pretty (indentation grows with depth), @dig (nested
// matches repeat) and @group (keys repeat per element). Past the limit the
// modifier yields no value, where gjson would keep allocating.
func outLimit(n int) int { return 64<<20 + 16*n }

// members walks the members of an object or the elements of an array.
//
// next gives each member with its value span. A depth-first walk instead
// uses start, descends into a container value, and hands the child's end to
// finish, so that no container is scanned twice.
type members struct {
	s   string
	i   int // where the next member starts; -1 when done
	end int // when done: the index after the closing bracket, or -1
	obj bool
}

// membersOf returns the walker for the container that opens at s[i].
func membersOf(s string, i int) members {
	return members{s: s, i: i + 1, end: -1, obj: s[i] == '{'}
}

// start reads the next member up to its value: the key span including
// quotes (empty for arrays) and the start of the value. The caller then
// calls finish with the end of the value.
func (m *members) start() (ks, ke, vs int, ok bool) {
	s, i := m.s, m.i
	if i < 0 {
		return
	}
	m.i = -1
	j := skipSpace(s, i)
	if j >= len(s) {
		return
	}
	if s[j] == '}' || s[j] == ']' {
		m.end = j + 1
		return
	}
	if !m.obj {
		return 0, 0, j, true
	}
	var good bool
	if ks, ke, vs, _, good = nextMember(s, j); !good {
		return 0, 0, 0, false
	}
	return ks, ke, vs, true
}

// finish moves past the member value that ends at s[ve], or ends the walk
// when ve is -1.
func (m *members) finish(ve int) {
	if ve < 0 {
		return
	}
	j := skipSpace(m.s, ve)
	if j >= len(m.s) {
		return
	}
	switch m.s[j] {
	case ',':
		m.i = j + 1
	case '}', ']':
		m.end = j + 1
	}
}

// next returns the next member: the key span including quotes (empty for
// arrays) and the value span.
func (m *members) next() (ks, ke, vs, ve int, ok bool) {
	if ks, ke, vs, ok = m.start(); !ok {
		return
	}
	if ve, _ = valueSpan(m.s, vs); ve <= vs {
		return 0, 0, 0, 0, false
	}
	m.finish(ve)
	return ks, ke, vs, ve, true
}

// root returns the start of the value in json and its first byte, or -1.
func root(json string) (int, byte) {
	i := skipSpace(json, 0)
	if i >= len(json) {
		return -1, 0
	}
	return i, json[i]
}

// decodedKey returns the decoded text of the key json[ks:ke], quotes
// included.
func decodedKey(json string, ks, ke int) string {
	raw := json[ks+1 : ke-1]
	if strings.IndexByte(raw, '\\') < 0 {
		return raw
	}
	b, _ := unescape(make([]byte, 0, len(raw)), raw)
	return bytesView(b)
}

// eachOption calls fn for each member of the JSON object arg, as the
// modifiers read their options.
func eachOption(arg string, fn func(key string, v Result)) {
	if arg == "" {
		return
	}
	Parse(arg).ForEach(func(k, v Result) bool {
		fn(k.Str, v)
		return true
	})
}

// modReverse reverses the elements of an array or the members of an object.
func modReverse(json string) string {
	i, c := root(json)
	if c != '[' && c != '{' {
		return json
	}
	var spans []int
	m := membersOf(json, i)
	for {
		ks, ke, vs, ve, ok := m.next()
		if !ok {
			break
		}
		if c == '{' {
			spans = append(spans, ks, ke)
		}
		spans = append(spans, vs, ve)
	}
	step := 2
	if c == '{' {
		step = 4
	}
	out := make([]byte, 0, len(json)-i)
	out = append(out, c)
	for k := len(spans) - step; k >= 0; k -= step {
		if len(out) > 1 {
			out = append(out, ',')
		}
		if c == '{' {
			out = append(out, json[spans[k]:spans[k+1]]...)
			out = append(out, ':')
			out = append(out, json[spans[k+2]:spans[k+3]]...)
		} else {
			out = append(out, json[spans[k]:spans[k+1]]...)
		}
	}
	return bytesView(append(out, c+2)) // ']' and '}' follow '[' and '{' by 2
}

// modUgly removes the whitespace outside strings. It reads 64-byte blocks
// with AVX2, else eight bytes at a time: the bytes inside strings follow
// from the quotes by a prefix XOR, and json is returned as it is when
// nothing is dropped. Blocks with backslashes go by words, and words with
// backslashes, the bytes after them and the last bytes one byte at a time.
// The output is written to a reused buffer and copied out at its final
// size.
func modUgly(json string) string {
	var out []byte // nil until the first byte to drop
	var bp *[]byte // the pooled buffer behind out, if any
	var st uglyState
	w, i := 0, 0
	for i+8 <= len(json) {
		if !st.esc {
			i, w = uglyBlocks(out, json, i, w, &st)
		}
		// Words up to the next block: the one uglyBlocks stopped before,
		// or every block without AVX2.
		end := min(i+64, len(json))
		if out == nil {
			if i = uglyCheck(json, i, end, &st); i+8 > end {
				continue
			}
			out, bp = uglyBuf(json, i)
			w = i
		}
		i, w = uglyWords(out, json, i, end, w, &st)
	}
	if out == nil {
		if !st.drops(json, i, len(json)) {
			return json
		}
		out, bp = uglyBuf(json, i)
		w = i
	}
	w = st.write(out, json, i, len(json), w)
	if bp == nil {
		return bytesView(out[:w])
	}
	res := string(out[:w])
	scratchPool.Put(bp)
	return res
}

// uglyCheck reads the words of json from i while they fit before end and
// @ugly keeps all their bytes. It returns the index of the first word with
// a byte to drop, or where it stopped, and st holds the state there.
func uglyCheck(json string, i, end int, st *uglyState) int {
	in := uint64(0) // lsb inside a string
	if st.in {
		in = lsb
	}
	for ; i+8 <= end; i += 8 {
		v := load64(json, i)
		if st.esc || exactZero(v^lsb*'\\') != 0 {
			st.in = in != 0
			if st.drops(json, i, i+8) {
				return i
			}
			in = 0
			if st.in {
				in = lsb
			}
			continue
		}
		// Byte k of x is 1 from an opening quote up to, not including, its
		// closing quote.
		x := exactZero(v^lsb*'"') >> 7
		x ^= x << 8
		x ^= x << 16
		x ^= x << 32
		x ^= in
		if ^(v&^msb+lsb*(0x80-0x21)|v)&msb&^(x<<7) != 0 {
			break
		}
		in = x >> 56 * lsb
	}
	st.in = in != 0
	return i
}

// uglyWords writes @ugly of the words of json from i that fit before end
// to out[w:]. It returns the new i and w.
func uglyWords(out []byte, json string, i, end, w int, st *uglyState) (int, int) {
	in := uint64(0)
	if st.in {
		in = lsb
	}
	for ; i+8 <= end; i += 8 {
		v := load64(json, i)
		if st.esc || exactZero(v^lsb*'\\') != 0 {
			st.in = in != 0
			w = st.write(out, json, i, i+8, w)
			in = 0
			if st.in {
				in = lsb
			}
			continue
		}
		x := exactZero(v^lsb*'"') >> 7
		x ^= x << 8
		x ^= x << 16
		x ^= x << 32
		x ^= in
		in = x >> 56 * lsb
		// Byte k of p counts the kept bytes up to byte k, so byte k+1
		// goes to o[byte k of p]. Every byte is stored; w never passes i,
		// so the eight bytes fit.
		p := (v&^msb + lsb*(0x80-0x21) | v | x<<7) & msb >> 7 * lsb
		o := (*[8]byte)(out[w : w+8])
		o[0] = byte(v)
		o[p&7] = byte(v >> 8)
		o[p>>8&7] = byte(v >> 16)
		o[p>>16&7] = byte(v >> 24)
		o[p>>24&7] = byte(v >> 32)
		o[p>>32&7] = byte(v >> 40)
		o[p>>40&7] = byte(v >> 48)
		o[p>>48&7] = byte(v >> 56)
		w += int(p >> 56)
	}
	st.in = in != 0
	return i, w
}

// scratchPool holds output buffers that modifiers reuse.
var scratchPool = sync.Pool{New: func() any { return new([]byte) }}

// uglyBuf returns the buffer @ugly writes json to, with json[:i] in place:
// from scratchPool, unless json is too big to keep, and then the pooled
// buffer.
func uglyBuf(json string, i int) ([]byte, *[]byte) {
	if len(json) > 1<<20 {
		out := make([]byte, len(json))
		copy(out, json[:i])
		return out, nil
	}
	bp := scratchPool.Get().(*[]byte)
	if cap(*bp) < len(json) {
		*bp = make([]byte, len(json))
	}
	out := (*bp)[:len(json)]
	copy(out, json[:i])
	return out, bp
}

// uglyState is where modUgly stands between words.
type uglyState struct {
	in  bool // inside a string
	esc bool // inside a string, after a backslash
}

// drops reports whether @ugly drops a byte of json[i:j]. If not, st moves
// to j.
func (st *uglyState) drops(json string, i, j int) bool {
	t := *st
	for ; i < j; i++ {
		c := json[i]
		switch {
		case t.esc:
			t.esc = false
		case t.in:
			t.esc, t.in = c == '\\', c != '"'
		case c <= ' ':
			return true
		case c == '"':
			t.in = true
		}
	}
	*st = t
	return false
}

// write is @ugly for json[i:j] one byte at a time, writing to out[w:]. It
// returns the new write position.
func (st *uglyState) write(out []byte, json string, i, j, w int) int {
	for ; i < j; i++ {
		c := json[i]
		switch {
		case st.esc:
			st.esc = false
		case st.in:
			st.esc, st.in = c == '\\', c != '"'
		case c <= ' ':
			continue
		case c == '"':
			st.in = true
		}
		out[w] = c
		w++
	}
	return w
}

// modKeys returns the keys of an object as an array. Each element of an
// array, and any other value, gives null.
func modKeys(json string) string {
	i, c := root(json)
	switch {
	case i < 0 || !Parse(json).Exists():
		return "[]"
	case c != '{' && c != '[':
		return "[null]"
	}
	var buf [64]int
	keys := buf[:0]
	size := 2
	m := membersOf(json, i)
	for {
		ks, ke, _, _, ok := m.next()
		if !ok {
			break
		}
		if c == '[' {
			ks, ke = 0, 4
		}
		keys = append(keys, ks, ke)
		size += ke - ks + 1
	}
	out := make([]byte, 0, size)
	out = append(out, '[')
	for k := 0; k < len(keys); k += 2 {
		if k > 0 {
			out = append(out, ',')
		}
		if c == '[' {
			out = append(out, "null"...)
		} else {
			out = append(out, json[keys[k]:keys[k+1]]...)
		}
	}
	return bytesView(append(out, ']'))
}

// modValues returns the values of an object as an array. An array is
// returned as is, and any other value is wrapped in an array.
func modValues(json string) string {
	r := Parse(json)
	switch {
	case r.IsArray():
		return json
	case !r.Exists():
		return "[]"
	case !r.IsObject():
		return "[" + r.Raw + "]"
	}
	i, _ := root(json)
	var buf [64]int
	vals := buf[:0]
	size := 2
	m := membersOf(json, i)
	for {
		_, _, vs, ve, ok := m.next()
		if !ok {
			break
		}
		vals = append(vals, vs, ve)
		size += ve - vs + 1
	}
	out := make([]byte, 0, size)
	out = append(out, '[')
	for k := 0; k < len(vals); k += 2 {
		if k > 0 {
			out = append(out, ',')
		}
		out = append(out, json[vals[k]:vals[k+1]]...)
	}
	return bytesView(append(out, ']'))
}

// inner returns the contents of the container raw without its brackets and
// the whitespace around them.
func inner(raw string) string {
	if len(raw) < 2 {
		return ""
	}
	lo, hi := trimSpace(raw[1 : len(raw)-1])
	return raw[1+lo : 1+hi]
}

// modFlatten merges the arrays inside an array into it. With
// {"deep":true}, nested arrays are merged at every depth.
func modFlatten(json, arg string) string {
	i, c := root(json)
	if c != '[' {
		return json
	}
	deep := false
	eachOption(arg, func(k string, v Result) {
		if k == "deep" {
			deep = v.Bool()
		}
	})
	out := []byte{'['}
	add := func(raw string) {
		if raw == "" {
			return
		}
		if len(out) > 1 {
			out = append(out, ',')
		}
		out = append(out, raw...)
	}
	m := membersOf(json, i)
	if !deep {
		for {
			_, _, vs, ve, ok := m.next()
			if !ok {
				break
			}
			if json[vs] == '[' {
				add(inner(json[vs:ve]))
			} else {
				add(json[vs:ve])
			}
		}
		return bytesView(append(out, ']'))
	}
	// Deep: every value that is not an array, in document order.
	var sbuf [16]members
	stack := append(sbuf[:0], m)
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		_, _, vs, ok := top.start()
		if !ok {
			end := top.end
			if stack = stack[:len(stack)-1]; len(stack) > 0 {
				stack[len(stack)-1].finish(end)
			}
			continue
		}
		if json[vs] == '[' {
			stack = append(stack, membersOf(json, vs))
			continue
		}
		ve, _ := valueSpan(json, vs)
		if ve <= vs {
			break
		}
		add(json[vs:ve])
		top.finish(ve)
	}
	return bytesView(append(out, ']'))
}

// modJoin merges the objects of an array into one object. A key seen again
// keeps its first position and takes the later value, unless
// {"preserve":true} keeps every member.
func modJoin(json, arg string) string {
	i, c := root(json)
	if c != '[' {
		return json
	}
	preserve := false
	eachOption(arg, func(k string, v Result) {
		if k == "preserve" {
			preserve = v.Bool()
		}
	})
	out := []byte{'{'}
	m := membersOf(json, i)
	if preserve {
		// Like gjson, the members are copied with the whitespace inside
		// the braces, and an empty object still adds a comma.
		for n := 0; ; {
			_, _, vs, ve, ok := m.next()
			if !ok {
				break
			}
			if json[vs] != '{' {
				continue
			}
			if n > 0 {
				out = append(out, ',')
			}
			out = append(out, json[vs+1:ve-1]...)
			n++
		}
		return bytesView(append(out, '}'))
	}
	type joined struct{ ks, ke, vs, ve int }
	var list []joined
	at := map[string]int{}
	for {
		_, _, os, _, ok := m.next()
		if !ok {
			break
		}
		if json[os] != '{' {
			continue
		}
		om := membersOf(json, os)
		for {
			ks, ke, vs, ve, ok := om.next()
			if !ok {
				break
			}
			k := decodedKey(json, ks, ke)
			if n, seen := at[k]; seen {
				list[n].vs, list[n].ve = vs, ve
				continue
			}
			at[k] = len(list)
			list = append(list, joined{ks, ke, vs, ve})
		}
	}
	for n, j := range list {
		if n > 0 {
			out = append(out, ',')
		}
		out = append(out, json[j.ks:j.ke]...)
		out = append(out, ':')
		out = append(out, json[j.vs:j.ve]...)
	}
	return bytesView(append(out, '}'))
}

// modGroup turns an object of arrays into an array of objects: object n
// holds element n of every array, under the array's key.
func modGroup(json string) string {
	i, c := root(json)
	if c != '{' {
		return ""
	}
	// Collect every element with its key and group, then write the groups
	// in order with one allocation.
	type item struct{ ks, ke, es, ee, g int }
	var buf [32]item
	items := buf[:0]
	groups := 0
	size := 2
	m := membersOf(json, i)
	for {
		ks, ke, vs, _, ok := m.next()
		if !ok {
			break
		}
		if json[vs] != '[' {
			continue
		}
		am := membersOf(json, vs)
		for n := 0; ; n++ {
			_, _, es, ee, ok := am.next()
			if !ok {
				break
			}
			if n == groups {
				groups++
				size += 3
			}
			items = append(items, item{ks, ke, es, ee, n})
			size += ke - ks + ee - es + 2
		}
	}
	if size > outLimit(len(json)) {
		return ""
	}
	if groups == 0 {
		return "[]"
	}
	// Order the items by group, keeping document order within each.
	at := make([]int, groups+1)
	for _, it := range items {
		at[it.g+1]++
	}
	for g := 1; g <= groups; g++ {
		at[g] += at[g-1]
	}
	order := make([]int32, len(items))
	for k, it := range items {
		order[at[it.g]] = int32(k)
		at[it.g]++
	}
	out := make([]byte, 0, size)
	out = append(out, '[')
	g := -1
	for _, k := range order {
		it := items[k]
		if it.g != g {
			if g >= 0 {
				out = append(out, '}', ',')
			}
			out = append(out, '{')
			g = it.g
		} else {
			out = append(out, ',')
		}
		out = append(out, json[it.ks:it.ke]...)
		out = append(out, ':')
		out = append(out, json[it.es:it.ee]...)
	}
	return bytesView(append(out, '}', ']'))
}

// modDig searches every value, in document order and parents first, and
// returns an array of the values that the path arg finds in them.
func modDig(json, arg string) string {
	i, c := root(json)
	var buf [16]string
	found := buf[:0]
	size, limit := 2, outLimit(len(json))
	visit := func(raw string) bool {
		if r := Get(raw, arg); r.Exists() {
			found = append(found, r.Raw)
			size += len(r.Raw) + 1
		}
		return size <= limit
	}
	// The root is visited as Parse reads it, even when empty: an object or
	// array runs to the end of json.
	if !visit(Parse(json).Raw) {
		return ""
	}
	if c == '{' || c == '[' {
		var sbuf [16]members
		stack := append(sbuf[:0], membersOf(json, i))
		for len(stack) > 0 {
			_, _, vs, ve, ok := stack[len(stack)-1].next()
			if !ok {
				stack = stack[:len(stack)-1]
				continue
			}
			if !visit(json[vs:ve]) {
				return ""
			}
			if json[vs] == '{' || json[vs] == '[' {
				stack = append(stack, membersOf(json, vs))
			}
		}
	}
	if len(found) == 0 {
		return "[]"
	}
	out := make([]byte, 0, size)
	out = append(out, '[')
	for k, raw := range found {
		if k > 0 {
			out = append(out, ',')
		}
		out = append(out, raw...)
	}
	return bytesView(append(out, ']'))
}

// prettyOpts are the @pretty options.
type prettyOpts struct {
	width          int
	prefix, indent string
	sortKeys       bool
}

// prettyScratch is the reusable memory of modPretty: the output, written
// here and copied out at its final size, and the arena of sorted members.
type prettyScratch struct {
	out   []byte
	arena []pmember
}

var prettyPool = sync.Pool{New: func() any { return new(prettyScratch) }}

// modPretty formats json for reading. The options are those of
// github.com/tidwall/pretty: {"sortKeys":bool,"indent":str,"prefix":str,
// "width":int}.
func modPretty(json, arg string) string {
	o := prettyOpts{width: 80, indent: "  "}
	eachOption(arg, func(k string, v Result) {
		switch k {
		case "sortKeys":
			o.sortKeys = v.Bool()
		case "indent":
			o.indent = onlySpace(v.String())
		case "prefix":
			o.prefix = onlySpace(v.String())
		case "width":
			o.width = int(v.Int())
		}
	})
	vs, _ := root(json)
	if vs < 0 {
		return ""
	}
	sc := prettyPool.Get().(*prettyScratch)
	p := printer{o: o, s: json, limit: outLimit(len(json)), budget: outLimit(len(json)), root: vs,
		arena: sc.arena[:0]}
	p.run = indentRun(o.prefix, o.indent)
	out := append(sc.out[:0], o.prefix...)
	out, ok := p.value(out, vs, 0, 0, false)
	res := ""
	if ok {
		// Input that is already in this form comes back as it is.
		if n := len(out); n+1 == len(json) && json[n] == '\n' && string(out) == json[:n] {
			res = json
		} else {
			res = string(append(out, '\n'))
		}
	}
	// Keep the memory for the next call, unless one big document would
	// hold it for good.
	if cap(out) <= 1<<20 && cap(p.arena) <= 1<<14 {
		sc.out, sc.arena = out[:0], p.arena[:0]
		prettyPool.Put(sc)
	}
	return res
}

// onlySpace keeps the whitespace bytes of s, so that the output stays JSON.
func onlySpace(s string) string {
	for i := 0; i < len(s); i++ {
		if !isSpace(s[i]) {
			b := make([]byte, 0, len(s))
			for j := 0; j < len(s); j++ {
				if isSpace(s[j]) {
					b = append(b, s[j])
				}
			}
			return bytesView(b)
		}
	}
	return s
}

var (
	nlSpaces = "\n" + strings.Repeat(" ", 128)
	nlTabs   = "\n" + strings.Repeat("\t", 64)
)

// indentRun returns a line break followed by a run of indentation when
// there is no prefix and indent repeats one space or tab, so that a new
// line is one copy; otherwise "".
func indentRun(prefix, indent string) string {
	if prefix != "" {
		return ""
	}
	run := nlSpaces
	if indent != "" && indent[0] == '\t' {
		run = nlTabs
	}
	for i := 0; i < len(indent); i++ {
		if indent[i] != run[1] {
			return ""
		}
	}
	return run
}

// printer writes the @pretty form of values of s.
type printer struct {
	o     prettyOpts
	s     string
	limit int
	run   string // see indentRun
	// nest counts the values being formatted to order members with equal
	// keys, and budget is the output those formattings may still take.
	// Past either bound, such members are ordered by their raw text.
	nest, budget int
	// arena holds the sorted members of the open objects, innermost last,
	// and keys the decoded keys with escapes.
	arena []pmember
	keys  []string
	// With sortKeys, an object's members are read up front, which needs
	// the end of each container value. scanned counts the bytes searched
	// for those ends; past a few times the input, as with deep nesting,
	// spans maps every container in the value at s[root] instead.
	scanned int
	spans   []cspan
	root    int
}

const maxNest = 32

// pmember is a member of an object being sorted by @pretty. Its key is
// p.s[ks+1:ke-1], or p.keys[kd] when the key has escapes. It holds no
// pointers, so that sorting moves plain memory.
type pmember struct {
	ks, ke, vs, ve, kd int
}

// key returns the decoded key of q.
func (p *printer) key(q *pmember) string {
	if q.kd < 0 {
		return p.s[q.ks+1 : q.ke-1]
	}
	return p.keys[q.kd]
}

// cspan is the start and end of a container.
type cspan struct{ start, end int }

// pframe is an open container in printer.value.
type pframe struct {
	m members
	// For sorted objects: the members are p.arena[next:stop], arena[base:]
	// belongs to the frame, and end is the index after the object.
	base, next, stop, end int
	obj, sorted           bool
}

// member returns the next member of f. For a sorted object, ve is the end
// of the value; otherwise it is 0 and the caller hands the end to
// f.m.finish.
func (p *printer) member(f *pframe) (ks, ke, vs, ve int, ok bool) {
	if !f.sorted {
		ks, ke, vs, ok = f.m.start()
		return ks, ke, vs, 0, ok
	}
	if f.next == f.stop {
		return
	}
	q := &p.arena[f.next]
	f.next++
	return q.ks, q.ke, q.vs, q.ve, true
}

// newline appends a line break with the prefix and depth indents, and
// returns the start of the new line.
func (p *printer) newline(dst []byte, depth int) ([]byte, int) {
	o := &p.o
	if n := depth * len(o.indent); n < len(p.run) {
		dst = append(dst, p.run[:n+1]...)
		return dst, len(dst) - n
	}
	dst = append(dst, '\n')
	line := len(dst)
	dst = append(dst, o.prefix...)
	for ; depth > 0; depth-- {
		dst = append(dst, o.indent...)
	}
	return dst, line
}

// value appends the pretty form of the value at s[vs]. The current output
// line starts at dst[line:], the value sits depth containers deep, and
// member reports whether it is an object member. It reports false when the
// output passes the limit.
//
// The walk is depth first with its own stack. Each container learns where
// it ends from its last member, so no value is scanned twice, except when
// sortKeys needs the members of an object up front.
func (p *printer) value(dst []byte, vs, line, depth int, member bool) ([]byte, bool) {
	s, o := p.s, &p.o
	start, mark := len(dst), len(p.arena)
	var buf [16]pframe
	stack := buf[:0]
	ks, ke := 0, 0
	// When an array decides between one line and many, its column counts
	// one more if it is an object member or the first element of an array,
	// as in tidwall/pretty.
	first := false
	for {
		// Write the member key, if any, then the value at s[vs], and find
		// where the value ends.
		if ke > ks {
			dst = append(dst, s[ks:ke]...)
			dst = append(dst, ':', ' ')
			member = true
		}
		end := -1
		switch c := s[vs]; c {
		case '{', '[':
			col := len(dst) - line
			if first || member {
				col++
			}
			if c == '[' && o.width > 0 {
				var fits bool
				if dst, end, fits = appendFit(dst, s, vs, o.width-col); fits {
					break
				}
			}
			f := pframe{m: membersOf(s, vs), obj: c == '{'}
			if f.obj && o.sortKeys {
				f.sorted, f.base = true, len(p.arena)
				f.end = p.sortMembers(vs, depth+len(stack)+1)
				f.next, f.stop = f.base, len(p.arena)
			}
			mk, mke, mvs, _, ok := p.member(&f)
			if !ok {
				dst = append(dst, c, c+2)
				end = f.m.end
				if f.sorted {
					end = f.end
					p.arena = p.arena[:f.base]
				}
				break
			}
			dst = append(dst, c)
			stack = append(stack, f)
			dst, line = p.newline(dst, depth+len(stack))
			ks, ke, vs = mk, mke, mvs
			first, member = true, false
			if len(dst)-start > p.limit {
				p.arena = p.arena[:mark]
				return dst, false
			}
			continue
		default:
			if end, _ = valueSpan(s, vs); end < vs {
				dst = append(dst, s[vs:]...)
			} else {
				dst = append(dst, s[vs:end]...)
			}
		}
		// Move to the next member, closing finished containers.
		for len(stack) > 0 {
			f := &stack[len(stack)-1]
			if !f.sorted {
				f.m.finish(end)
			}
			var ok bool
			if ks, ke, vs, _, ok = p.member(f); ok {
				dst = append(dst, ',')
				dst, line = p.newline(dst, depth+len(stack))
				first, member = false, false
				break
			}
			c := byte('}')
			if !f.obj {
				c = ']'
			}
			end = f.m.end
			if f.sorted {
				end = f.end
				p.arena = p.arena[:f.base]
			}
			stack = stack[:len(stack)-1]
			dst, line = p.newline(dst, depth+len(stack))
			dst = append(dst, c)
		}
		if len(dst)-start > p.limit {
			p.arena = p.arena[:mark]
			return dst, false
		}
		if len(stack) == 0 {
			return dst, true
		}
	}
}

// sortMembers appends the members of the object at s[i], whose members sit
// depth containers deep, to p.arena, sorted by decoded key, and returns the
// index after the object, or -1. Like tidwall/pretty, members with equal
// keys are ordered by their formatted values.
func (p *printer) sortMembers(i, depth int) int {
	s := p.s
	base := len(p.arena)
	m := membersOf(s, i)
	for {
		ks, ke, vs, ok := m.start()
		if !ok {
			break
		}
		var ve int
		if c := s[vs]; c == '{' || c == '[' {
			ve = p.containerEnd(vs)
		} else {
			ve, _ = valueSpan(s, vs)
		}
		if ve <= vs {
			break
		}
		m.finish(ve)
		kd := -1
		if strings.IndexByte(s[ks+1:ke-1], '\\') >= 0 {
			kd = len(p.keys)
			p.keys = append(p.keys, decodedKey(s, ks, ke))
		}
		p.arena = append(p.arena, pmember{ks, ke, vs, ve, kd})
	}
	slices.SortStableFunc(p.arena[base:], func(a, b pmember) int { return strings.Compare(p.key(&a), p.key(&b)) })
	for a := base; a < len(p.arena); {
		b := a + 1
		for b < len(p.arena) && p.key(&p.arena[b]) == p.key(&p.arena[a]) {
			b++
		}
		if b-a > 1 {
			p.sortEqual(a, b, depth)
		}
		a = b
	}
	return m.end
}

// containerEnd returns the index after the container at s[i], or -1.
func (p *printer) containerEnd(i int) int {
	if p.spans == nil {
		e := containerEnd(p.s, i)
		if e < 0 {
			p.scanned += len(p.s) - i
		} else {
			p.scanned += e - i
		}
		if p.scanned <= 4*len(p.s)+1<<16 {
			return e
		}
		p.spans = containerSpans(p.s, p.root)
	}
	k, found := slices.BinarySearchFunc(p.spans, i, func(c cspan, i int) int { return c.start - i })
	if !found {
		return -1
	}
	return p.spans[k].end
}

// containerSpans returns the spans of every object and array inside the
// value at s[i], by start; an unterminated one ends at -1.
func containerSpans(s string, i int) []cspan {
	spans := []cspan{}
	var open []int
	for ; i < len(s); i++ {
		switch s[i] {
		case '"':
			e := stringEnd(s, i+1)
			if e < 0 {
				return spans
			}
			i = e - 1
		case '{', '[':
			open = append(open, len(spans))
			spans = append(spans, cspan{i, -1})
		case '}', ']':
			if len(open) == 0 {
				return spans
			}
			spans[open[len(open)-1]].end = i + 1
			if open = open[:len(open)-1]; len(open) == 0 {
				return spans
			}
		}
	}
	return spans
}

// sortEqual orders p.arena[a:b], members with equal keys, by their
// formatted values: by type (null, false, number, string, true, then
// objects and arrays), then numbers by value, strings by decoded text and
// the rest by text.
func (p *printer) sortEqual(a, b, depth int) {
	n := b - a
	texts := make([]string, n)
	containers := 0
	for k := range texts {
		q := p.arena[a+k]
		texts[k] = p.s[q.vs:q.ve]
		if c := p.s[q.vs]; c == '{' || c == '[' {
			containers++
		}
	}
	for k := range texts {
		// Other values are formatted as they are, and only two objects or
		// arrays need their formatted text to compare.
		q := p.arena[a+k]
		if c := p.s[q.vs]; (c != '{' && c != '[') || containers < 2 || p.nest >= maxNest || p.budget <= 0 {
			continue
		}
		// The value is formatted where it would stand: after the prefix,
		// the indentation and its key. A nested formatting may take half
		// the output of the one around it, so together they stay within
		// twice the limit; every attempt counts against the budget. It
		// may grow p.arena, so members are read again after it.
		var buf []byte
		buf = append(buf, p.o.prefix...)
		for i := depth; i > 0; i-- {
			buf = append(buf, p.o.indent...)
		}
		buf = append(buf, p.s[q.ks:q.ke]...)
		buf = append(buf, ':', ' ')
		at := len(buf)
		limit := p.limit
		p.limit = min(limit/2, p.budget)
		p.nest++
		out, ok := p.value(buf, q.vs, 0, depth, true)
		p.nest--
		p.limit = limit
		p.budget -= len(out)
		if ok {
			texts[k] = bytesView(out[at:])
		}
	}
	idx := make([]int, n)
	for k := range idx {
		idx[k] = k
	}
	slices.SortStableFunc(idx, func(x, y int) int {
		switch {
		case lessFormatted(texts[x], texts[y]):
			return -1
		case lessFormatted(texts[y], texts[x]):
			return 1
		}
		return 0
	})
	sorted := make([]pmember, n)
	for k, i := range idx {
		sorted[k] = p.arena[a+i]
	}
	copy(p.arena[a:b], sorted)
}

// lessFormatted compares two formatted values for sortEqual.
func lessFormatted(a, b string) bool {
	ta, tb := textType(a), textType(b)
	if ta != tb {
		return ta < tb
	}
	switch ta {
	case String:
		return Parse(a).Str < Parse(b).Str
	case Number:
		fa, _ := strconv.ParseFloat(a, 64)
		fb, _ := strconv.ParseFloat(b, 64)
		return fa < fb
	}
	return a < b
}

// textType returns the type of the value that the text t starts with.
func textType(t string) Type {
	if t == "" {
		return Null
	}
	switch c := t[0]; {
	case c == '"':
		return String
	case c == 't':
		return True
	case c == 'f':
		return False
	case c == '{' || c == '[':
		return JSON
	case c == '-' || (c >= '0' && c <= '9'):
		return Number
	}
	return Null
}

// appendFit appends the one-line form of the array at s[i], with ", "
// between elements, and returns the index after the array, if more than 3
// bytes are left on the line, the array holds no object, and its one-line
// form takes at most max bytes. Otherwise it returns dst as it was and
// false.
func appendFit(dst []byte, s string, i, max int) ([]byte, int, bool) {
	if max <= 3 {
		return dst, -1, false
	}
	start, depth := len(dst), 0
	for ; i < len(s); i++ {
		if len(dst)-start > max {
			break
		}
		switch c := s[i]; c {
		case ' ', '\t', '\n', '\r':
		case '"':
			e := stringEnd(s, i+1)
			if e < 0 || len(dst)-start+e-i > max {
				return dst[:start], -1, false
			}
			dst = append(dst, s[i:e]...)
			i = e - 1
		case '{':
			return dst[:start], -1, false
		case ',':
			dst = append(dst, ',', ' ')
		case '[':
			depth++
			dst = append(dst, c)
		case ']':
			depth--
			dst = append(dst, c)
			if depth == 0 {
				if len(dst)-start > max {
					return dst[:start], -1, false
				}
				return dst, i + 1, true
			}
		default:
			dst = append(dst, c)
		}
	}
	return dst[:start], -1, false
}
