package bench

import (
	"strings"
	"sync"
)

// A case is one pattern, one operation and its inputs. Classes cover
// A short request fields, B text replacement, C redaction and keyword lists,
// D structured text extraction, E streaming frames, F source search,
// G mixed text and binary payloads, and H general patterns.
type benchCase struct {
	class, name, pat, op string
	in                   []string
}

const (
	opMatch    = "match"
	opFind     = "find"
	opFindAll  = "findall"
	opSubmatch = "submatch" // FindStringSubmatchIndex
	opSubAll   = "suball"   // FindAllStringSubmatchIndex
	opReplace  = "replace"  // ReplaceAllString with repl
)

var (
	casesOnce sync.Once
	allCases  []benchCase
)

func one(s string) []string { return []string{s} }

func cases() []benchCase {
	casesOnce.Do(buildCases)
	return allCases
}

func buildCases() {
	allCases = casesForClass("")
}

// casesForClass builds only the inputs needed by class. An empty class
// selects every case. This keeps unrelated fixtures out of memory samples.
func casesForClass(class string) []benchCase {
	want := func(classes string) bool { return class == "" || strings.Contains(classes, class) }
	models := []string{"gpt-4o-mini-2024-07-18", "claude-sonnet-4-5-20250929", "gemini-2.5-pro", "o3-mini-high", "deepseek-r1-0528", "qwen3-235b-a22b-thinking-2507", "gpt-5-codex", "claude-opus-4-1-thinking"}
	headers := []string{"Content-Type", "Authorization", "X-Request-Id", "X-Trace-Id", "User-Agent", "Accept", "X-Stainless-Os", "X-Stainless-Lang", "Anthropic-Version", "Anthropic-Beta", "Openai-Organization", "X-Forwarded-For", "Accept-Encoding", "Connection", "X-Api-Key", "Cookie"}
	paths := []string{"messages.-1.content", "messages.0.content.-2.text", "tools.3.function.name", "metadata.user_id", "input.-1"}
	kw := Keywords(200, 7)
	quoted := make([]string, len(kw))
	for i, k := range kw {
		quoted[i] = k
	}
	var p64, sec64, sec1m, log4, out64, src1m, img, letters, ranged, sens4, sens64 string
	var sse []string
	if want("BCGH") {
		p64 = Prose(64<<10, 1, 0.3)
	}
	sens := SensitiveWords(1000, 10)
	if want("C") {
		sec64, sec1m, log4 = WithSecrets(64<<10, 2), WithSecrets(1<<20, 3), LogText(4<<10, 4)
		sens4, sens64 = SensitiveText(4<<10, 11, sens), SensitiveText(64<<10, 12, sens)
	}
	if want("BD") {
		out64 = ModelOutput(64<<10, 5)
	}
	if want("F") {
		src1m = Source(1<<20, 6)
	}
	if want("G") {
		img = RequestWithImage(8<<10, 2<<20, 8)
	}
	if want("E") {
		sse = SSEFrames(64, 9)
	}
	if want("H") {
		letters, ranged = p64[:16<<10], p64[:4<<10]
	}
	cs := []benchCase{
		{"A", "model-anchored-alt", `^(gpt-[a-z0-9][a-z0-9._-]*|o[1-9][a-z0-9._-]*)$`, opMatch, models},
		{"A", "model-admin-wild", `^claude-.*-thinking$`, opMatch, models},
		{"A", "model-admin-contains", `(?i)deepseek|qwen3`, opMatch, models},
		{"A", "header-pass", `(?i)^x-(request|trace)-id$`, opMatch, headers},
		{"A", "slug-validator", `^[a-zA-Z0-9_-]+$`, opMatch, one("my_plugin-key_2026-v1")},
		{"A", "semver-validator", `^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`, opMatch, one("1.12.3-beta.1+build.5")},
		{"A", "url-validator", `^https?://(?:(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?|(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?))(?::[0-9]{1,5})?(?:/[^\s]*)?$`, opMatch, one("https://api.example.com:8443/v1/chat/completions")},
		{"A", "neg-index-path", `\.(-\d+)`, opSubAll, paths},
		{"B", "ws-run-64k", `\s+`, opReplace, one(p64)},
		{"B", "word-fold-64k", `(?i)\bmodel\b`, opReplace, one(p64)},
		{"B", "think-block-64k", `(?s)<think>.*?</think>`, opReplace, one(out64)},
		{"C", "mask-url-4k", `(?:https?|wss?)://[^\s"<>{}]+`, opReplace, one(log4)},
		{"C", "mask-ipv4-4k", `\b(?:\d{1,3}\.){3}\d{1,3}\b`, opReplace, one(log4)},
		{"C", "mask-ipv6-4k", `\[([0-9A-Fa-f:.]*:[0-9A-Fa-f:.]*)(%[^\]\s]*)?\]`, opReplace, one(log4)},
		{"C", "mask-apikey-4k", `(['"]?)api_key:([^\s'"]+)(['"]?)`, opReplace, one(log4)},
		{"C", "sk-key-1m", `sk-[A-Za-z0-9_-]{20,}`, opFindAll, one(sec1m)},
		{"C", "email-1m", `[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`, opFindAll, one(sec1m)},
		{"C", "jwt-64k", `eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`, opFindAll, one(sec64)},
		{"C", "cn-phone-64k", `\b1[3-9]\d{9}\b`, opFindAll, one(sec64)},
		{"C", "pii-union-64k", `sk-[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+|[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}|\b1[3-9]\d{9}\b`, opFindAll, one(sec64)},
		{"C", "keywords200-64k", `(?i)(` + strings.Join(quoted, "|") + `)`, opFindAll, one(p64)},
		{"C", "sensitive-filter-4k", SensitivePattern(sens), opReplace, one(sens4)},
		{"C", "sensitive-filter-64k", SensitivePattern(sens), opReplace, one(sens64)},
		{"D", "think-64k", `(?s)<think>(.*?)</think>`, opSubmatch, one(out64)},
		{"D", "toolcall-64k", `(?s)<tool_call>\s*(.*?)\s*</tool_call>`, opSubAll, one(out64)},
		{"D", "fence-64k", "(?s)```(\\w*)\\n(.*?)```", opSubAll, one(out64)},
		{"D", "react-action-64k", `(?m)^Action:\s*(.+)$`, opSubAll, one(out64)},
		{"D", "json-greedy-64k", `(?s)\{.*\}`, opSubmatch, one(out64)},
		{"E", "sse-data", `^data: (.*)$`, opSubmatch, sse},
		{"E", "sse-content", `"content":"((?:[^"\\]|\\.)*)"`, opSubmatch, sse},
		{"E", "sse-done", `^data: \[DONE\]$`, opMatch, sse},
		{"F", "literal-1m", `errors.New`, opFindAll, one(src1m)},
		{"F", "literal-rare-1m", `TODO: handle`, opFindAll, one(src1m)},
		{"F", "fold-1m", `(?i)error`, opFindAll, one(src1m)},
		{"F", "func-decl-1m", `(?m)^func\s+\(\w+\s+\*?\w+\)\s+\w+`, opFindAll, one(src1m)},
		{"F", "ident-alt-1m", `\b(?:Handler|Writer|Buffer|Token|Relay)\d+\b`, opFindAll, one(src1m)},
		{"F", "nomatch-1m", `ZZZ[0-9]+QQQ`, opFindAll, one(src1m)},
		{"G", "sk-in-image-req", `sk-[A-Za-z0-9]{32,}`, opFindAll, one(img)},
		{"G", "b64-run-in-image-req", `[A-Za-z0-9+/]{64,}={0,2}`, opFindAll, one(img)},
		{"G", "han-run-64k", `\p{Han}+`, opFindAll, one(p64)},
		{"H", "letters-16k", `\pL+`, opFindAll, one(letters)},
		{"H", "stdlib-hard-64k", `[ -~]*ABCDEFGHIJKLMNOPQRSTUVWXYZ$`, opMatch, one(p64)},
		{"H", "patho-aa-32k", `(a|aa)*b`, opMatch, one(strings.Repeat("a", 32<<10))},
		{"H", "range-1000-4k", `[a-z]{1,1000}`, opFindAll, one(ranged)},
	}
	if class == "" {
		return cs
	}
	var selected []benchCase
	for _, c := range cs {
		if c.class == class {
			selected = append(selected, c)
		}
	}
	return selected
}

// replacements holds the replacement templates of the opReplace cases.
var replacements = map[string]string{
	"ws-run-64k":      " ",
	"word-fold-64k":   "engine",
	"think-block-64k": "",
	"mask-url-4k":     "***",
	"mask-ipv4-4k":    "***.***.***.***",
	"mask-ipv6-4k":    "[***]",
	"mask-apikey-4k":  "${1}api_key:***${3}",

	"sensitive-filter-4k":  "***",
	"sensitive-filter-64k": "***",
}

func run(e engine, c *benchCase, s string) any {
	switch c.op {
	case opMatch:
		return e.MatchString(s)
	case opFind:
		return e.FindStringIndex(s)
	case opFindAll:
		return e.FindAllStringIndex(s, -1)
	case opSubmatch:
		return e.FindStringSubmatchIndex(s)
	case opSubAll:
		return e.FindAllStringSubmatchIndex(s, -1)
	case opReplace:
		return e.ReplaceAllString(s, replacements[c.name])
	}
	panic(c.op)
}
