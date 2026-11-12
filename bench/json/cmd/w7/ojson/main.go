// Command ojson runs the W7 scenarios of moego/ojson, the one JSON package
// it links. Its lines name the engine moego.
//
//	validr     ojson.ValidReader on the file
//	getmany    reads the file into memory and calls ojson.GetManyBytes
//	getmanyr   ojson.GetManyReaderAt on the file
//
// See github.com/MoeForge/moego/bench/json/w7.
package main

import (
	"fmt"
	"os"

	"github.com/MoeForge/moego/bench/json/w7"
	"github.com/MoeForge/moego/ojson"
)

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func main() {
	w7.Main(w7.Engine{Name: "moego", Own: map[string]func(file string) any{
		"validr": func(file string) any {
			f, err := os.Open(file)
			check(err)
			defer f.Close()
			ok, err := ojson.ValidReader(f)
			check(err)
			if !ok {
				check(fmt.Errorf("not valid"))
			}
			return nil
		},
		"getmany": func(file string) any {
			b, err := os.ReadFile(file)
			check(err)
			return ojson.GetManyBytes(b, "model", "group")
		},
		"getmanyr": func(file string) any {
			f, err := os.Open(file)
			check(err)
			defer f.Close()
			st, err := f.Stat()
			check(err)
			res, err := ojson.GetManyReaderAt(f, st.Size(), "model", "group")
			check(err)
			return res
		},
	}})
}
