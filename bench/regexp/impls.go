package bench

import (
	stdre "regexp"

	moego "github.com/MoeForge/moego/regexp"
	"github.com/coregx/coregex"
	grafana "github.com/grafana/regexp"
	re2 "github.com/wasilibs/go-re2"
)

// engine is the part of the regexp API the benchmarks exercise. Every
// implementation is called through it in the same way.
type engine interface {
	MatchString(string) bool
	FindStringIndex(string) []int
	FindAllStringIndex(string, int) [][]int
	FindStringSubmatchIndex(string) []int
	FindAllStringSubmatchIndex(string, int) [][]int
	ReplaceAllString(string, string) string
}

type impl struct {
	name    string
	compile func(string) (engine, error)
}

var impls = []impl{
	{"Stdlib", func(p string) (engine, error) { return stdre.Compile(p) }},
	{"Grafana", func(p string) (engine, error) { return grafana.Compile(p) }},
	{"Coregex", func(p string) (engine, error) { return coregex.Compile(p) }},
	{"GoRE2", func(p string) (engine, error) { return re2.Compile(p) }},
	{"Moego", func(p string) (engine, error) { return moego.Compile(p) }},
}
