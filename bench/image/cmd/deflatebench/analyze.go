package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// streamStats describes the blocks and symbols of a zlib stream.
type streamStats struct {
	blocks          [3]int // stored, fixed, dynamic
	headerBits      int
	literals        int
	litBits         int
	matches         int
	matchBits       int
	matchBytes      int
	lengths         map[int]int
	distBuckets     [16]int // by bit length of the distance
	blockSymbols    []int
	totalBits, size int
}

type bitReader struct {
	b   []byte
	pos int // bit position
}

func (r *bitReader) bits(n int) (int, error) {
	v := 0
	for k := 0; k < n; k++ {
		byteIdx := r.pos >> 3
		if byteIdx >= len(r.b) {
			return 0, io.ErrUnexpectedEOF
		}
		v |= int(r.b[byteIdx]>>(r.pos&7)&1) << k
		r.pos++
	}
	return v, nil
}

// huffDec decodes a canonical code bit by bit.
type huffDec struct {
	count  [16]int
	symbol []int
}

func newHuffDec(lens []int) *huffDec {
	h := &huffDec{}
	for _, l := range lens {
		h.count[l]++
	}
	h.count[0] = 0
	offs := [16]int{}
	for l := 1; l < 16; l++ {
		offs[l] = offs[l-1] + h.count[l-1]
	}
	h.symbol = make([]int, len(lens))
	for s, l := range lens {
		if l != 0 {
			h.symbol[offs[l]] = s
			offs[l]++
		}
	}
	return h
}

func (h *huffDec) decode(r *bitReader) (sym, nbits int, err error) {
	code, first, index := 0, 0, 0
	for l := 1; l < 16; l++ {
		b, err := r.bits(1)
		if err != nil {
			return 0, 0, err
		}
		code |= b
		count := h.count[l]
		if code-count < first {
			return h.symbol[index+(code-first)], l, nil
		}
		index += count
		first += count
		first <<= 1
		code <<= 1
	}
	return 0, 0, errors.New("bad code")
}

var (
	lenBase   = [29]int{3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258}
	lenExtraB = [29]int{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0}
	distBaseT = [30]int{1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577}
	distExtra = [30]int{0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13}
	clOrder   = [19]int{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}
)

// analyze walks the zlib stream z.
func analyze(z []byte) (*streamStats, error) {
	st := &streamStats{lengths: map[int]int{}, size: len(z)}
	r := &bitReader{b: z[2 : len(z)-4]}
	for {
		final, err := r.bits(1)
		if err != nil {
			return nil, err
		}
		typ, _ := r.bits(2)
		st.blocks[typ]++
		syms := 0
		var lit, dist *huffDec
		switch typ {
		case 0:
			r.pos = (r.pos + 7) &^ 7
			n, _ := r.bits(16)
			r.bits(16)
			r.pos += 8 * n
			st.literals += n
			st.litBits += 8 * n
			st.blockSymbols = append(st.blockSymbols, n)
			if final == 1 {
				st.totalBits = r.pos
				return st, nil
			}
			continue
		case 1:
			lens := make([]int, 288)
			for i := range lens {
				switch {
				case i < 144:
					lens[i] = 8
				case i < 256:
					lens[i] = 9
				case i < 280:
					lens[i] = 7
				default:
					lens[i] = 8
				}
			}
			lit = newHuffDec(lens)
			dl := make([]int, 30)
			for i := range dl {
				dl[i] = 5
			}
			dist = newHuffDec(dl)
		case 2:
			start := r.pos
			hlit, _ := r.bits(5)
			hdist, _ := r.bits(5)
			hclen, _ := r.bits(4)
			cl := make([]int, 19)
			for i := 0; i < hclen+4; i++ {
				cl[clOrder[i]], _ = r.bits(3)
			}
			clh := newHuffDec(cl)
			lens := make([]int, 0, hlit+257+hdist+1)
			for len(lens) < hlit+257+hdist+1 {
				s, _, err := clh.decode(r)
				if err != nil {
					return nil, err
				}
				switch {
				case s < 16:
					lens = append(lens, s)
				case s == 16:
					n, _ := r.bits(2)
					for k := 0; k < 3+n; k++ {
						lens = append(lens, lens[len(lens)-1])
					}
				case s == 17:
					n, _ := r.bits(3)
					lens = append(lens, make([]int, 3+n)...)
				default:
					n, _ := r.bits(7)
					lens = append(lens, make([]int, 11+n)...)
				}
			}
			lit = newHuffDec(lens[:hlit+257])
			dist = newHuffDec(lens[hlit+257:])
			st.headerBits += r.pos - start
		default:
			return nil, errors.New("bad block type")
		}
		for {
			s, nb, err := lit.decode(r)
			if err != nil {
				return nil, err
			}
			syms++
			if s < 256 {
				st.literals++
				st.litBits += nb
				continue
			}
			if s == 256 {
				break
			}
			l := lenBase[s-257]
			e, _ := r.bits(lenExtraB[s-257])
			l += e
			ds, db, err := dist.decode(r)
			if err != nil {
				return nil, err
			}
			de, _ := r.bits(distExtra[ds])
			d := distBaseT[ds] + de
			st.matches++
			st.matchBits += nb + lenExtraB[s-257] + db + distExtra[ds]
			st.matchBytes += l
			st.lengths[min(l, 32)]++
			k := 0
			for 1<<k < d {
				k++
			}
			st.distBuckets[k]++
		}
		st.blockSymbols = append(st.blockSymbols, syms)
		if final == 1 {
			st.totalBits = r.pos
			return st, nil
		}
	}
}

func (st *streamStats) print(name string) {
	fmt.Fprintf(os.Stdout, "%s: %d bytes, blocks stored/fixed/dynamic %v, header %d bytes\n", name, st.size, st.blocks, st.headerBits/8)
	fmt.Printf("  literals %d (%.3f bits each, %d bytes)  matches %d covering %d bytes (%.2f bits each, %d bytes)\n",
		st.literals, float64(st.litBits)/float64(max(st.literals, 1)), st.litBits/8, st.matches, st.matchBytes, float64(st.matchBits)/float64(max(st.matches, 1)), st.matchBits/8)
	var ls []int
	for l := range st.lengths {
		ls = append(ls, l)
	}
	sort.Ints(ls)
	fmt.Print("  lengths:")
	for _, l := range ls {
		fmt.Printf(" %d:%d", l, st.lengths[l])
	}
	fmt.Print("\n  distance bits:")
	for k, n := range st.distBuckets {
		fmt.Printf(" %d:%d", k, n)
	}
	sort.Ints(st.blockSymbols)
	if n := len(st.blockSymbols); n > 0 {
		fmt.Printf("\n  block symbols: min %d median %d max %d\n", st.blockSymbols[0], st.blockSymbols[n/2], st.blockSymbols[n-1])
	}
}
