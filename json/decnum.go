package json

import (
	"math"
	"math/bits"
	"strconv"
	"unsafe"

	"github.com/MoeForge/moego/internal/jsonsimd"
)

// pow10 holds the powers of ten a float64 represents exactly.
var pow10 = [...]float64{1, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10,
	1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19, 1e20, 1e21, 1e22}

var pow10f32 = [...]float32{1, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10}

// floatAt parses the number at s[i] in one pass and returns its value and
// its end. It reports false for a number with an exponent, or too many
// digits to be exact in a float64, which one division can not round
// correctly; a syntax error stops a fused decode. For bitSize 32 the value
// goes through a float64, which is the correct float32 unless it falls
// exactly between two of them.
func (d *decodeState) floatAt(i, bitSize int) (f float64, end int, ok bool) {
	s := d.s
	neg := s[i] == '-'
	if neg {
		i++
	}
	var m uint64
	j := i
	for j < len(s) && s[j]-'0' < 10 {
		m = m*10 + uint64(s[j]-'0')
		j++
	}
	nd := j - i
	if nd == 0 || s[i] == '0' && nd > 1 {
		d.bad()
	}
	k := 0
	if j < len(s) && s[j] == '.' {
		j++
		from := j
		for len(s)-j >= 8 {
			w := jsonsimd.Load64(s, j) ^ asciiZeros
			if stop := (w + 0x7676767676767676 | w) & 0x8080808080808080; stop != 0 {
				// Fewer than eight digits: move them to the top and let
				// zero bytes lead.
				n := bits.TrailingZeros64(stop) >> 3
				if n > 0 {
					m = m*pow10u[n] + parse8(w<<(64-8*uint(n)))
					j += n
				}
				break
			}
			m = m*1e8 + parse8(w)
			j += 8
			if j-from > 24 {
				return 0, 0, false
			}
		}
		for j < len(s) && s[j]-'0' < 10 {
			m = m*10 + uint64(s[j]-'0')
			j++
		}
		if j == from {
			d.bad()
		}
		k = j - from
		nd += k
	}
	if j < len(s) && s[j]|0x20 == 'e' || nd > 19 || m > 1<<53 || k > 22 {
		return 0, 0, false
	}
	f = float64(m) / pow10[k]
	if bitSize == 32 && math.Float64bits(f)&(1<<29-1) == 1<<28 {
		return 0, 0, false
	}
	if neg {
		f = -f
	}
	return f, j, true
}

// pow10u holds the powers of ten below 1e8 as integers.
var pow10u = [8]uint64{1, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7}

// parse8 returns the number whose eight decimal digits are the bytes of w,
// the first digit in the lowest byte.
func parse8(w uint64) uint64 {
	w = (w*10 + w>>8) & 0x00ff00ff00ff00ff
	w = (w * (1 + 100<<16)) >> 16 & 0x0000ffff0000ffff
	return (w * (1 + 10000<<32)) >> 32
}

// parseFloatAt parses the valid JSON number d.s[start:end] as
// strconv.ParseFloat does with the given bit size, and reports whether it
// is in range.
func (d *decodeState) parseFloatAt(start, end, bitSize int) (float64, bool) {
	s := d.s[start:end]
	if f, ok := fastFloat(s, bitSize); ok {
		return f, true
	}
	f, err := strconv.ParseFloat(s, bitSize)
	return f, err == nil
}

// fastFloat parses a JSON number whose decimal mantissa and power of ten
// are both exact in the target type, so one multiplication or division
// rounds correctly. It reports false for other numbers.
func fastFloat(s string, bitSize int) (float64, bool) {
	i := 0
	neg := s[0] == '-'
	if neg {
		i++
	}
	var m uint64
	nd, exp := 0, 0
	for ; i < len(s); i++ {
		c := s[i] - '0'
		if c >= 10 {
			break
		}
		m = m*10 + uint64(c)
		nd++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for ; i < len(s); i++ {
			c := s[i] - '0'
			if c >= 10 {
				break
			}
			m = m*10 + uint64(c)
			nd++
			exp--
		}
	}
	if i < len(s) {
		// An exponent.
		i++
		eneg := false
		if s[i] == '+' || s[i] == '-' {
			eneg = s[i] == '-'
			i++
		}
		e := 0
		for ; i < len(s); i++ {
			if e < 10000 {
				e = e*10 + int(s[i]-'0')
			}
		}
		if eneg {
			e = -e
		}
		exp += e
	}
	if nd > 19 {
		return 0, false
	}
	if bitSize == 32 {
		if m > 1<<24 || exp < -10 || exp > 10 {
			return 0, false
		}
		f := float32(m)
		if exp < 0 {
			f /= pow10f32[-exp]
		} else {
			f *= pow10f32[exp]
		}
		if neg {
			f = -f
		}
		return float64(f), true
	}
	if m > 1<<53 || exp < -22 || exp > 22 {
		return 0, false
	}
	f := float64(m)
	if exp < 0 {
		f /= pow10[-exp]
	} else {
		f *= pow10[exp]
	}
	if neg {
		f = -f
	}
	return f, true
}

// intValue decodes the number at d.i into the integer at p of plan pl.
func (d *decodeState) intValue(pl *decPlan, p unsafe.Pointer) {
	s := d.s
	start := d.i
	i := start
	neg := s[i] == '-'
	if neg {
		i++
	}
	var m uint64
	j := i
	for j < len(s) && s[j]-'0' < 10 {
		m = m*10 + uint64(s[j]-'0')
		j++
	}
	nd := j - i
	if nd == 0 || s[i] == '0' && nd > 1 {
		d.bad()
	}
	end := j
	isInt := true
	if end < len(s) && (s[end] == '.' || s[end]|0x20 == 'e') {
		isInt = false
		end = d.numberEnd(start)
	}
	d.i = end
	if !isInt || nd > 18 {
		d.intSlow(pl, p, start, end)
		return
	}
	if k := pl.kind; k == dInt64 || k == dInt && strconv.IntSize == 64 {
		n := int64(m)
		if neg {
			n = -n
		}
		*(*int64)(p) = n
		return
	}
	switch pl.kind {
	case dInt, dInt8, dInt16, dInt32, dInt64:
		n := int64(m)
		if neg {
			n = -n
		}
		if overflowInt(pl.kind, n) {
			d.typeError("number "+s[start:end], pl.typ, end)
			return
		}
		storeInt(pl.kind, p, n)
	default:
		if neg || overflowUint(pl.kind, m) {
			d.typeError("number "+s[start:end], pl.typ, end)
			return
		}
		storeUint(pl.kind, p, m)
	}
}

// intSlow decodes the number d.s[start:end] into an integer with strconv.
func (d *decodeState) intSlow(pl *decPlan, p unsafe.Pointer, start, end int) {
	lit := d.s[start:end]
	switch pl.kind {
	case dInt, dInt8, dInt16, dInt32, dInt64:
		n, err := strconv.ParseInt(lit, 10, 64)
		if err != nil || overflowInt(pl.kind, n) {
			d.typeError("number "+lit, pl.typ, end)
			return
		}
		storeInt(pl.kind, p, n)
	default:
		n, err := strconv.ParseUint(lit, 10, 64)
		if err != nil || overflowUint(pl.kind, n) {
			d.typeError("number "+lit, pl.typ, end)
			return
		}
		storeUint(pl.kind, p, n)
	}
}

func overflowInt(k uint8, n int64) bool {
	switch k {
	case dInt8:
		return n < math.MinInt8 || n > math.MaxInt8
	case dInt16:
		return n < math.MinInt16 || n > math.MaxInt16
	case dInt32:
		return n < math.MinInt32 || n > math.MaxInt32
	case dInt:
		return strconv.IntSize == 32 && (n < math.MinInt32 || n > math.MaxInt32)
	}
	return false
}

func overflowUint(k uint8, n uint64) bool {
	switch k {
	case dUint8:
		return n > math.MaxUint8
	case dUint16:
		return n > math.MaxUint16
	case dUint32:
		return n > math.MaxUint32
	case dUint, dUintptr:
		return strconv.IntSize == 32 && n > math.MaxUint32
	}
	return false
}

func overflowFloat(k uint8, f float64) bool {
	if f < 0 {
		f = -f
	}
	if k == dFloat32 {
		return math.MaxFloat32 < f && f <= math.Inf(1)
	}
	return math.MaxFloat64 < f && f <= math.Inf(1)
}

func storeInt(k uint8, p unsafe.Pointer, n int64) {
	switch k {
	case dInt8:
		*(*int8)(p) = int8(n)
	case dInt16:
		*(*int16)(p) = int16(n)
	case dInt32:
		*(*int32)(p) = int32(n)
	case dInt:
		*(*int)(p) = int(n)
	default:
		*(*int64)(p) = n
	}
}

func storeUint(k uint8, p unsafe.Pointer, n uint64) {
	switch k {
	case dUint8:
		*(*uint8)(p) = uint8(n)
	case dUint16:
		*(*uint16)(p) = uint16(n)
	case dUint32:
		*(*uint32)(p) = uint32(n)
	case dUint:
		*(*uint)(p) = uint(n)
	case dUintptr:
		*(*uintptr)(p) = uintptr(n)
	default:
		*(*uint64)(p) = n
	}
}

// isValidNumber reports whether s is a JSON number literal.
func isValidNumber(s []byte) bool {
	if len(s) == 0 {
		return false
	}
	i := 0
	if s[0] == '-' {
		i++
		if i == len(s) {
			return false
		}
	}
	switch {
	case s[i] == '0':
		i++
	case '1' <= s[i] && s[i] <= '9':
		for i++; i < len(s) && s[i]-'0' < 10; i++ {
		}
	default:
		return false
	}
	if i+1 < len(s) && s[i] == '.' && s[i+1]-'0' < 10 {
		for i += 2; i < len(s) && s[i]-'0' < 10; i++ {
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if i == len(s) || s[i]-'0' >= 10 {
			return false
		}
		for ; i < len(s) && s[i]-'0' < 10; i++ {
		}
	}
	return i == len(s)
}
