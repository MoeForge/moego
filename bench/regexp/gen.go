package bench

import (
	"encoding/base64"
	"fmt"
	"math/rand/v2"
	"strings"
	"unicode/utf8"
)

// Deterministic synthetic input generators. No real user data.

var enWords = strings.Fields(`the a to of and in is that for it as with on be this you are can not by
from or have an will your which at if we use when more data model request response token context
function value error user server client stream message tool result output input file code test
config agent gateway channel provider latency retry timeout upstream cache prompt answer reason`)

var zhWords = []string{"我们", "模型", "请求", "上下文", "工具", "调用", "结果", "用户", "服务", "网关",
	"渠道", "推理", "输出", "输入", "文件", "代码", "测试", "配置", "代理", "延迟", "重试", "超时",
	"缓存", "提示词", "回答", "原因", "错误", "数据", "函数", "参数"}

// cut truncates to at most n bytes on a rune boundary (inputs stay valid UTF-8).
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func rng(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) }

// Prose: mixed English/Chinese paragraphs with occasional markdown, numbers and URLs.
func Prose(n int, seed uint64, zhRatio float64) string {
	r := rng(seed)
	var b strings.Builder
	b.Grow(n + 512)
	for b.Len() < n {
		words := 8 + r.IntN(20)
		for i := 0; i < words; i++ {
			if r.Float64() < zhRatio {
				b.WriteString(zhWords[r.IntN(len(zhWords))])
			} else {
				if i > 0 {
					b.WriteByte(' ')
				}
				w := enWords[r.IntN(len(enWords))]
				if r.IntN(12) == 0 {
					w = strings.ToUpper(w[:1]) + w[1:]
				}
				b.WriteString(w)
			}
		}
		switch r.IntN(10) {
		case 0:
			b.WriteString("。\n\n")
		case 1:
			fmt.Fprintf(&b, " see https://docs.example.com/v%d/%s?id=%d.\n", r.IntN(4), enWords[r.IntN(len(enWords))], r.IntN(99999))
		case 2:
			fmt.Fprintf(&b, " (%d.%d%%, %d ms)", r.IntN(100), r.IntN(10), r.IntN(5000))
		default:
			b.WriteString(". ")
		}
	}
	return cut(b.String(), n)
}

const alnum = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func randTok(r *rand.Rand, n int, alphabet string) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.IntN(len(alphabet))]
	}
	return string(b)
}

// WithSecrets: prose with sparse PII and secrets (synthetic, fake).
func WithSecrets(n int, seed uint64) string {
	r := rng(seed)
	p := Prose(n, seed+1, 0.3)
	var b strings.Builder
	b.Grow(n + 8192)
	for i := 0; i < len(p) && b.Len() < n; {
		step := 1500 + r.IntN(3000)
		if i+step > len(p) {
			step = len(p) - i
		}
		b.WriteString(p[i : i+step])
		i += step
		switch r.IntN(7) {
		case 0:
			b.WriteString(" key sk-" + randTok(r, 48, alnum) + " ")
		case 1:
			b.WriteString(" mail " + randTok(r, 7, "abcdefghij") + "@example.com ")
		case 2:
			fmt.Fprintf(&b, " phone 1%d%09d ", 3+r.IntN(7), r.IntN(1e9))
		case 3:
			b.WriteString(" jwt eyJ" + randTok(r, 30, alnum) + "." + randTok(r, 60, alnum) + "." + randTok(r, 40, alnum) + " ")
		case 4:
			fmt.Fprintf(&b, " ip 10.%d.%d.%d ", r.IntN(256), r.IntN(256), r.IntN(256))
		case 5:
			b.WriteString(" AKIA" + randTok(r, 16, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") + " ")
		}
	}
	return cut(b.String(), n)
}

// LogText: gateway error/log lines.
func LogText(n int, seed uint64) string {
	r := rng(seed)
	var b strings.Builder
	b.Grow(n + 512)
	for b.Len() < n {
		fmt.Fprintf(&b, "2026-11-04T12:%02d:%02d.%03dZ ERROR relay channel=%d: upstream error: Post \"https://api%d.example.com/v1/chat/completions\": dial tcp 10.%d.%d.%d:443: i/o timeout; api_key:sk-%s retry=%d\n",
			r.IntN(60), r.IntN(60), r.IntN(1000), r.IntN(500), r.IntN(9), r.IntN(256), r.IntN(256), r.IntN(256), randTok(r, 24, alnum), r.IntN(4))
		if r.IntN(4) == 0 {
			fmt.Fprintf(&b, "2026-11-04T12:00:00Z WARN dial tcp [2001:db8::%x]:443: connection refused\n", r.IntN(65536))
		}
	}
	return cut(b.String(), n)
}

// ModelOutput: reasoning model output with think block, prose, code fences and tool calls.
func ModelOutput(n int, seed uint64) string {
	r := rng(seed)
	var b strings.Builder
	b.Grow(n + 2048)
	b.WriteString("<think>\n")
	b.WriteString(Prose(n/3, seed+2, 0.2))
	b.WriteString("\n</think>\n\n")
	for b.Len() < n {
		b.WriteString(Prose(400+r.IntN(1200), seed+uint64(b.Len()), 0.2))
		switch r.IntN(4) {
		case 0:
			b.WriteString("\n```go\nfunc handle(w http.ResponseWriter, r *http.Request) {\n\tif err := process(r); err != nil {\n\t\thttp.Error(w, err.Error(), 500)\n\t}\n}\n```\n")
		case 1:
			fmt.Fprintf(&b, "\n<tool_call>\n{\"name\": \"search_files\", \"arguments\": {\"query\": \"%s\", \"limit\": %d}}\n</tool_call>\n", enWords[r.IntN(len(enWords))], r.IntN(50))
		case 2:
			fmt.Fprintf(&b, "\nThought: I should check the %s.\nAction: read_file\nAction Input: {\"path\": \"src/%s.go\"}\nObservation: ok\n", enWords[r.IntN(len(enWords))], enWords[r.IntN(len(enWords))])
		}
	}
	return cut(b.String(), n)
}

// SSEFrame: one streaming chunk line.
func SSEFrames(count int, seed uint64) []string {
	r := rng(seed)
	out := make([]string, count)
	for i := range out {
		out[i] = fmt.Sprintf(`data: {"id":"chatcmpl-%s","object":"chat.completion.chunk","created":1762257600,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"%s"},"finish_reason":null}]}`,
			randTok(r, 24, alnum), Prose(4+r.IntN(30), seed+uint64(i), 0.3))
	}
	return out
}

// Source: synthetic Go-like source code for grep workloads.
func Source(n int, seed uint64) string {
	r := rng(seed)
	ids := []string{"Handler", "Request", "Response", "Context", "Writer", "Reader", "Buffer", "Config", "Server", "Client", "Channel", "Token", "Stream", "Relay", "Model"}
	var b strings.Builder
	b.Grow(n + 512)
	for b.Len() < n {
		a, c := ids[r.IntN(len(ids))], ids[r.IntN(len(ids))]
		fmt.Fprintf(&b, "// %s%s handles %s.\nfunc (s *%s) %s%d(ctx context.Context, req *%s) (*%s, error) {\n\tif req == nil {\n\t\treturn nil, errors.New(\"nil %s\")\n\t}\n\tv := s.cache[%q]\n\tfor i := 0; i < len(v); i++ {\n\t\ts.total += v[i] * %d\n\t}\n\treturn &%s{ID: %d}, nil\n}\n\n",
			a, c, enWords[r.IntN(len(enWords))], a, c, r.IntN(1000), a, c, enWords[r.IntN(len(enWords))], enWords[r.IntN(len(enWords))], r.IntN(100), c, r.IntN(1e6))
		if r.IntN(40) == 0 {
			b.WriteString("\t// TODO: handle Error paths\n")
		}
	}
	return cut(b.String(), n)
}

// RequestWithImage: chat request JSON with escaped text and an embedded base64 image.
func RequestWithImage(textN, imgN int, seed uint64) string {
	r := rng(seed)
	text := strings.ReplaceAll(Prose(textN, seed, 0.3), "\n", `\n`)
	const prefix = `{"model":"gpt-4o","stream":true,"messages":[{"role":"system","content":"You are a helpful assistant."},{"role":"user","content":[{"type":"text","text":"`
	const middle = `"},{"type":"image_url","image_url":{"url":"data:image/png;base64,`
	const suffix = `"}}]}]}`
	var b strings.Builder
	b.Grow(len(prefix) + len(text) + len(middle) + base64.StdEncoding.EncodedLen(imgN*3/4) + len(suffix))
	b.WriteString(prefix)
	b.WriteString(text)
	b.WriteString(middle)
	encoder := base64.NewEncoder(base64.StdEncoding, &b)
	var block [768]byte
	for left := imgN * 3 / 4; left > 0; {
		n := min(left, len(block))
		for i := 0; i < n; i++ {
			block[i] = byte(r.Uint32())
		}
		encoder.Write(block[:n])
		left -= n
	}
	encoder.Close()
	b.WriteString(suffix)
	return b.String()
}

// Keywords: content-safety style keyword list, mixed languages.
func Keywords(n int, seed uint64) []string {
	r := rng(seed)
	out := make([]string, n)
	for i := range out {
		if i%3 == 0 {
			out[i] = zhWords[r.IntN(len(zhWords))] + zhWords[r.IntN(len(zhWords))] + "违规"
		} else {
			out[i] = randTok(r, 5+r.IntN(6), "abcdefghijklmnopqrstuvwxyz")
		}
	}
	return out
}

// sensitiveHan are the characters of SensitiveWords' Chinese words:
// neutral nouns (nature, plants, animals, objects), none of which Prose
// writes.
const sensitiveHan = "山川湖岛林森松柏竹梅兰菊荷莲桃李杏梨橘柚瓜豆麦稻禾米茶糖盐灯烛纸墨砚笔琴棋鼓笛钟铃窗墙瓦砖石玉珠铜锡舟桥塔亭阁楼园田井泉溪潭峰岭谷坡沙泥冰霜雪雾云霞虹星辰晨暮春夏秋冬晴鹤鹰燕雀鸽鹅鱼虾蟹鹿马牛羊兔猫熊象蝶蜂蚕"

// sensitiveRoots are the neutral dictionary words that SensitiveWords'
// English words are made of.
var sensitiveRoots = strings.Fields(`amber birch cedar coral delta ember fern fjord garnet harbor hazel heron
iris ivory jade juniper kelp lark lily lotus maple marble meadow nectar oak olive onyx opal orchid otter
pebble pine plum poppy quail quartz raven reed river robin ruby sable saffron sage salmon sierra spruce
tide topaz tulip umber velvet violet walnut willow wren yarrow zephyr zinc ash elm fig ivy yew bay`)

// SensitiveWords: a content-moderation keyword list of n synthetic, neutral
// words, alternately Chinese (2 to 4 characters of sensitiveHan) and English
// (3 to 12 letters: one or two of sensitiveRoots). No word occurs in
// Prose's text, so SensitiveText decides every hit.
func SensitiveWords(n int, seed uint64) []string {
	r := rng(seed)
	han := []rune(sensitiveHan)
	var vocab strings.Builder
	for _, w := range enWords {
		vocab.WriteString(w + " ")
	}
	vocab.WriteString("see https docs example com v id ms")
	seen := map[string]bool{}
	out := make([]string, 0, n)
	for len(out) < n {
		var w string
		if len(out)%2 == 0 {
			k := make([]rune, 2+r.IntN(3))
			for i := range k {
				k[i] = han[r.IntN(len(han))]
			}
			w = string(k)
		} else {
			w = sensitiveRoots[r.IntN(len(sensitiveRoots))]
			if r.IntN(8) != 0 {
				w += sensitiveRoots[r.IntN(len(sensitiveRoots))]
			}
			if len(w) > 12 || strings.Contains(vocab.String(), w) {
				continue
			}
		}
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// SensitivePattern: the case-insensitive alternation of words.
func SensitivePattern(words []string) string {
	return `(?i)(?:` + strings.Join(words, "|") + `)`
}

// SensitiveText: chat prose of n bytes with about one of words per KiB,
// English words in lower, title or upper case.
func SensitiveText(n int, seed uint64, words []string) string {
	r := rng(seed)
	var b strings.Builder
	b.Grow(n + 2048)
	for i := uint64(1); b.Len() < n; i++ {
		b.WriteString(Prose(700+r.IntN(600), seed+i, 0.3))
		w := words[r.IntN(len(words))]
		switch r.IntN(4) {
		case 0:
			w = strings.ToUpper(w)
		case 1:
			w = strings.ToUpper(w[:1]) + w[1:]
		}
		b.WriteString(" " + w + " ")
	}
	return cut(b.String(), n)
}
