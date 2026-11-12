package codec

import (
	"fmt"
	"math/rand"
	"strings"
	"unicode/utf16"
)

// LongChats are the long-context text requests: a coding agent's
// conversation sized by the o200k_base tokens of its text (the system
// prompt, the turns, the tool call arguments and the tool results),
// counted with tiktoken (tokens.py). N is the body size in bytes that
// gives that count: 100Ktok is 376782 bytes with 99720 tokens of text,
// 256Ktok is 961597 bytes with 254892.
var LongChats = []struct {
	Name string
	N    int
}{{"100Ktok", 374000}, {"256Ktok", 960000}}

var longEn = strings.Fields(`the a an to of and in is that for it as with on be this you are can not by from or
have will your which at if we use when more data model request response token context function value
error user server client stream message tool result output input file code test config agent gateway
channel provider latency retry timeout upstream cache prompt answer reason handler buffer queue worker
limit quota billing usage header body chunk decoder encoder schema field struct pointer slice map
string number array object nil empty first last next after before because so then also only still
each every some most all both other same new old small large fast slow safe broken fixed changed
returns reads writes keeps drops skips checks parses copies sends waits fails passes runs builds`)

var longZh = []string{"我们", "先看一下", "这个请求", "上下文", "工具调用", "返回结果", "用户", "服务端", "网关",
	"渠道", "推理模型", "输出", "输入", "文件", "代码", "测试", "配置项", "代理", "延迟", "重试", "超时",
	"缓存命中", "提示词", "回答", "原因是", "错误", "数据", "函数", "参数", "流式响应", "计费", "额度",
	"并发", "日志", "这里", "需要注意", "已经修复", "没有问题", "可以复现", "改成", "然后", "因为", "所以",
	"如果", "但是", "目前", "接口", "字段", "结构体", "切片", "指针", "序列化", "反序列化", "性能", "内存"}

var longIdents = strings.Fields(`relay channel stream chunk usage quota token billing handler request response
upstream retry timeout cache key value buf state cfg ctx err msg tool call args result model group user`)

var longFiles = strings.Fields(`relay/channel/openai/adaptor.go relay/channel/claude/relay.go relay/common/stream.go
service/quota.go service/token_counter.go controller/relay.go middleware/distributor.go model/channel.go
common/json.go dto/openai_request.go dto/claude.go relay/helper/stream_scanner.go setting/ratio.go`)

var longEmoji = []string{"✅", "⚠️", "🚀", "📌", "🔧", "❌", "→", "…", "été", "東京", "naïve", "Größe"}

// longGen writes the conversation of LongChatBody.
type longGen struct {
	r *rand.Rand
	b strings.Builder
	t strings.Builder // the text of the message being built
}

func (g *longGen) pick(words []string) string { return words[g.r.Intn(len(words))] }

func (g *longGen) ident() string {
	s := g.pick(longIdents)
	if g.r.Intn(2) == 0 {
		s += title(g.pick(longIdents))
	}
	return s
}

func title(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

// sentence writes one English or Chinese sentence, with code spans, paths
// and numbers here and there.
func (g *longGen) sentence() {
	t := &g.t
	if g.r.Intn(3) == 0 {
		for i, n := 0, 3+g.r.Intn(8); i < n; i++ {
			t.WriteString(g.pick(longZh))
			if g.r.Intn(9) == 0 {
				fmt.Fprintf(t, " `%s` ", g.ident())
			}
			if i == n/2 && g.r.Intn(2) == 0 {
				t.WriteString("，")
			}
		}
		t.WriteString("。")
		return
	}
	n := 6 + g.r.Intn(16)
	for i := 0; i < n; i++ {
		if i > 0 {
			t.WriteByte(' ')
		}
		w := g.pick(longEn)
		if i == 0 {
			w = title(w)
		}
		switch g.r.Intn(30) {
		case 0:
			w = "`" + g.ident() + "`"
		case 1:
			w = g.pick(longFiles)
		case 2:
			w = fmt.Sprintf("%d", g.r.Intn(5000))
		case 3:
			w = `"` + w + `"`
		case 4:
			w = g.pick(longEmoji)
		}
		t.WriteString(w)
	}
	t.WriteString(". ")
}

func (g *longGen) paragraph() {
	for i, n := 0, 2+g.r.Intn(5); i < n; i++ {
		g.sentence()
	}
	g.t.WriteString("\n\n")
}

// code writes n lines of Go, Python or shell.
func (g *longGen) code(n int) {
	t := &g.t
	switch g.r.Intn(4) {
	case 0:
		t.WriteString("```python\n")
		fmt.Fprintf(t, "def %s(%s, %s=None):\n", g.ident(), g.ident(), g.ident())
		for i := 0; i < n; i++ {
			switch g.r.Intn(5) {
			case 0:
				fmt.Fprintf(t, "    %s = re.compile(r\"^data: (\\{.*\\})$\")\n", g.ident())
			case 1:
				fmt.Fprintf(t, "    if not %s.get(\"%s\"):\n        raise ValueError(f\"missing %s: {%s!r}\")\n", g.ident(), g.ident(), g.pick(longEn), g.ident())
			case 2:
				fmt.Fprintf(t, "    # %s%s\n", g.pick(longZh), g.pick(longZh))
			default:
				fmt.Fprintf(t, "    %s = json.loads(%s)[\"%s\"][%d]\n", g.ident(), g.ident(), g.ident(), g.r.Intn(8))
			}
		}
		fmt.Fprintf(t, "    return %s\n```\n\n", g.ident())
	case 1:
		t.WriteString("```bash\n")
		for i := 0; i < n/3+1; i++ {
			fmt.Fprintf(t, "curl -sS -H \"Authorization: Bearer $KEY\" -d '{\"model\":\"gpt-4o\",\"stream\":true}' http://127.0.0.1:3000/v1/chat/completions | grep -c '^data: '\n")
			fmt.Fprintf(t, "go test -run 'Test%s' -count=1 ./%s/...\n", title(g.ident()), strings.Split(g.pick(longFiles), "/")[0])
		}
		t.WriteString("```\n\n")
	default:
		t.WriteString("```go\n")
		fmt.Fprintf(t, "func (h *%sHandler) %s(ctx context.Context, %s []byte) (*Response, error) {\n", title(g.ident()), title(g.ident()), g.ident())
		for i := 0; i < n; i++ {
			switch g.r.Intn(6) {
			case 0:
				fmt.Fprintf(t, "\tif err := json.Unmarshal(%s, &%s); err != nil {\n\t\treturn nil, fmt.Errorf(\"decode %s: %%w\", err)\n\t}\n", g.ident(), g.ident(), g.ident())
			case 1:
				fmt.Fprintf(t, "\t// %s，%s\n", g.pick(longZh), g.pick(longZh))
			case 2:
				fmt.Fprintf(t, "\tfor i, %s := range %s.%s {\n\t\t%s[i] = strings.TrimPrefix(%s, \"data: \")\n\t}\n", g.ident(), g.ident(), g.ident(), g.ident(), g.ident())
			case 3:
				fmt.Fprintf(t, "\tlog.Printf(\"%s=%%d %s=%%q\\n\", %s, %s)\n", g.ident(), g.ident(), g.ident(), g.ident())
			default:
				fmt.Fprintf(t, "\t%s := %s.%s(%s, %d)\n", g.ident(), g.ident(), g.ident(), g.ident(), g.r.Intn(1024))
			}
		}
		t.WriteString("\treturn resp, nil\n}\n```\n\n")
	}
}

// message writes the text built in g.t as a message of role. One message
// in eight escapes its non-ASCII text, as clients that serialize with
// ensure_ascii do.
func (g *longGen) message(role, extra string) {
	fmt.Fprintf(&g.b, `,{"role":%q,"content":`, role)
	writeJSONString(&g.b, g.t.String(), g.r.Intn(8) == 0)
	g.b.WriteString(extra)
	g.b.WriteByte('}')
	g.t.Reset()
}

// writeJSONString writes s as a JSON string. With ascii set it writes every
// non-ASCII rune as a \u escape.
func writeJSONString(b *strings.Builder, s string, ascii bool) {
	b.WriteByte('"')
	for _, c := range s {
		switch {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteRune(c)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20:
			fmt.Fprintf(b, `\u%04x`, c)
		case ascii && c >= 0x10000:
			r1, r2 := utf16.EncodeRune(c)
			fmt.Fprintf(b, `\u%04x\u%04x`, r1, r2)
		case ascii && c >= 0x80:
			fmt.Fprintf(b, `\u%04x`, c)
		default:
			b.WriteRune(c)
		}
	}
	b.WriteByte('"')
}

const longAlnum = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func (g *longGen) callID() string {
	id := make([]byte, 24)
	for i := range id {
		id[i] = longAlnum[g.r.Intn(len(longAlnum))]
	}
	return "call_" + string(id)
}

// toolRound writes an assistant message with one or two tool calls and a
// tool message with the result of each.
func (g *longGen) toolRound() {
	type call struct{ id, name, args string }
	calls := make([]call, 1+g.r.Intn(2))
	for i := range calls {
		c := &calls[i]
		c.id = g.callID()
		switch g.r.Intn(3) {
		case 0:
			c.name, c.args = "read_file", fmt.Sprintf(`{"path":%q,"offset":%d,"limit":%d}`, g.pick(longFiles), g.r.Intn(400), 50+g.r.Intn(200))
		case 1:
			c.name, c.args = "grep", fmt.Sprintf(`{"pattern":"func \\(.*\\) %s\\(","path":%q}`, g.ident(), strings.Split(g.pick(longFiles), "/")[0])
		default:
			c.name, c.args = "run_command", fmt.Sprintf(`{"command":"go test -run 'Test%s' -count=1 ./...","timeout_ms":%d}`, g.ident(), 1000*(30+g.r.Intn(90)))
		}
	}
	var tc strings.Builder
	tc.WriteString(`,"tool_calls":[`)
	for i, c := range calls {
		if i > 0 {
			tc.WriteByte(',')
		}
		fmt.Fprintf(&tc, `{"id":%q,"type":"function","function":{"name":%q,"arguments":`, c.id, c.name)
		writeJSONString(&tc, c.args, false)
		tc.WriteString(`}}`)
	}
	tc.WriteByte(']')
	if g.r.Intn(2) == 0 {
		g.sentence()
	}
	g.message("assistant", tc.String())
	for _, c := range calls {
		switch c.name {
		case "read_file":
			for i, n := 0, 40+g.r.Intn(160); i < n; i++ {
				fmt.Fprintf(&g.t, "%5d\t", i+1)
				switch g.r.Intn(4) {
				case 0:
					fmt.Fprintf(&g.t, "\t%s := %s.%s(%q)\n", g.ident(), g.ident(), g.ident(), g.pick(longEn))
				case 1:
					fmt.Fprintf(&g.t, "\t// %s %s %s\n", g.pick(longEn), g.pick(longZh), g.pick(longEn))
				case 2:
					fmt.Fprintf(&g.t, "\tif %s == nil || len(%s.%s) == 0 {\n", g.ident(), g.ident(), g.ident())
				default:
					fmt.Fprintf(&g.t, "\treturn %s.%s(ctx, \"%s\\n\", %d)\n", g.ident(), g.ident(), g.ident(), g.r.Intn(100))
				}
			}
		case "grep":
			for i, n := 0, 5+g.r.Intn(30); i < n; i++ {
				fmt.Fprintf(&g.t, "%s:%d:func (a *%s) %s(c *gin.Context, info *RelayInfo) error {\n", g.pick(longFiles), 10+g.r.Intn(900), g.ident(), g.ident())
			}
		default:
			for i, n := 0, 5+g.r.Intn(40); i < n; i++ {
				if g.r.Intn(12) == 0 {
					fmt.Fprintf(&g.t, "--- FAIL: Test%s (0.%02ds)\n    %s:%d: got \"%s\", want \"%s\"\n", g.ident(), g.r.Intn(100), g.pick(longFiles), g.r.Intn(500), g.pick(longEn), g.pick(longEn))
				} else {
					fmt.Fprintf(&g.t, "=== RUN   Test%s\n--- PASS: Test%s (0.%02ds)\n", g.ident(), g.ident(), g.r.Intn(100))
				}
			}
			fmt.Fprintf(&g.t, "ok  \tgithub.com/example/gateway/%s\t%d.%03ds\n", g.ident(), g.r.Intn(9), g.r.Intn(1000))
		}
		g.message("tool", fmt.Sprintf(`,"tool_call_id":%q`, c.id))
	}
}

// LongChatBody returns a text chat request of about n bytes: a system
// prompt, then user and assistant turns in English and Chinese with code
// blocks, and every fourth turn or so a round of tool calls and results.
// Bodies are deterministic.
func LongChatBody(n int) []byte {
	g := &longGen{r: rand.New(rand.NewSource(20261107))}
	b := &g.b
	b.WriteString(`{"model":"gpt-4.1","stream":true,"temperature":0.2,"max_tokens":8192,"messages":[{"role":"system","content":`)
	g.t.WriteString("You are a coding agent working in the repository of an LLM gateway. Read files before you change them, run the tests after each change, and answer in the user's language.\n\n")
	for i := 0; i < 6; i++ {
		g.paragraph()
	}
	writeJSONString(b, g.t.String(), false)
	b.WriteByte('}')
	g.t.Reset()
	const tail = `],"tools":[` +
		`{"type":"function","function":{"name":"read_file","description":"Reads lines of a file in the repository.","parameters":{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":2000}},"required":["path"]}}},` +
		`{"type":"function","function":{"name":"grep","description":"Searches the repository with a regular expression.","parameters":{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"}},"required":["pattern"]}}},` +
		`{"type":"function","function":{"name":"run_command","description":"Runs a shell command and returns its output.","parameters":{"type":"object","properties":{"command":{"type":"string"},"timeout_ms":{"type":"integer"}},"required":["command"]}}}` +
		`],"tool_choice":"auto","stream_options":{"include_usage":true}}`
	for turn := 0; b.Len() < n-len(tail); turn++ {
		g.paragraph()
		if g.r.Intn(3) == 0 {
			g.code(4 + g.r.Intn(20))
		}
		g.message("user", "")
		if g.r.Intn(4) == 0 {
			g.toolRound()
		}
		for i, k := 0, 1+g.r.Intn(4); i < k; i++ {
			g.paragraph()
			if g.r.Intn(2) == 0 {
				g.code(6 + g.r.Intn(30))
			}
		}
		g.message("assistant", "")
	}
	b.WriteString(tail)
	return []byte(b.String())
}
