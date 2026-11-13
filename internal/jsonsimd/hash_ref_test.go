package jsonsimd

import (
	"bytes"
	"crypto/aes"
	"encoding/binary"
	"encoding/hex"
	"hash/maphash"
	"math/rand"
	"testing"
)

// refSbox is the AES S-box, built from inverses in GF(2^8) and the affine
// map of FIPS 197.
var refSbox = func() (s [256]byte) {
	for x := range 256 {
		// x^254 is the inverse of x, and 0 for 0.
		inv := byte(1)
		for range 254 {
			inv = refMul(inv, byte(x))
		}
		b := inv
		s[x] = b ^ b<<1 ^ b>>7 ^ b<<2 ^ b>>6 ^ b<<3 ^ b>>5 ^ b<<4 ^ b>>4 ^ 0x63
	}
	return
}()

// refMul multiplies in GF(2^8) modulo x^8+x^4+x^3+x+1.
func refMul(a, b byte) byte {
	var p byte
	for ; b != 0; b >>= 1 {
		if b&1 != 0 {
			p ^= a
		}
		a = a<<1 ^ 0x1b*(a>>7)
	}
	return p
}

// refRound is AESENC, with mix, and AESENCLAST, without: ShiftRows,
// SubBytes, MixColumns if mix, and the round key XORed in. Byte r+4c of
// the state is row r of column c.
func refRound(s, rk [16]byte, mix bool) [16]byte {
	var t [16]byte
	for c := range 4 {
		for r := range 4 {
			t[r+4*c] = refSbox[s[r+4*((c+r)%4)]]
		}
	}
	if mix {
		for c := range 4 {
			a0, a1, a2, a3 := t[4*c], t[4*c+1], t[4*c+2], t[4*c+3]
			t[4*c] = refMul(a0, 2) ^ refMul(a1, 3) ^ a2 ^ a3
			t[4*c+1] = a0 ^ refMul(a1, 2) ^ refMul(a2, 3) ^ a3
			t[4*c+2] = a0 ^ a1 ^ refMul(a2, 2) ^ refMul(a3, 3)
			t[4*c+3] = refMul(a0, 3) ^ a1 ^ a2 ^ refMul(a3, 2)
		}
	}
	for i := range t {
		t[i] ^= rk[i]
	}
	return t
}

func refEnc(s, rk [16]byte) [16]byte { return refRound(s, rk, true) }

// refAES128 encrypts in under key with refRound: the first round key XORed
// in, nine AESENC and one AESENCLAST, with the key schedule of FIPS 197.
func refAES128(key, in [16]byte) [16]byte {
	var w [176]byte
	copy(w[:], key[:])
	rcon := byte(1)
	for i := 16; i < 176; i += 4 {
		t := [4]byte(w[i-4 : i])
		if i%16 == 0 {
			t = [4]byte{refSbox[t[1]] ^ rcon, refSbox[t[2]], refSbox[t[3]], refSbox[t[0]]}
			rcon = refMul(rcon, 2)
		}
		for j := range 4 {
			w[i+j] = w[i-16+j] ^ t[j]
		}
	}
	s := in
	for i := range s {
		s[i] ^= w[i]
	}
	for r := 1; r < 10; r++ {
		s = refEnc(s, [16]byte(w[16*r:]))
	}
	return refRound(s, [16]byte(w[160:]), false)
}

// refHashAES is hashAES in Go, over all of b, which has 128 bytes or more:
// Sum's result where the CPU has AES.
func refHashAES(key *[24]uint64, b []byte) uint64 {
	var kb [192]byte
	for i, k := range key {
		binary.LittleEndian.PutUint64(kb[8*i:], k)
	}
	var l [8][16]byte
	for i := range l {
		l[i] = [16]byte(kb[16*i:])
	}
	n := len(b) &^ 127
	for p := 0; p < n; p += 128 {
		for i := range l {
			l[i] = refEnc(l[i], [16]byte(b[p+16*i:]))
		}
	}
	var tail [128]byte
	copy(tail[:], b[n:])
	for i := range l {
		rk := [16]byte(tail[16*i:])
		for j := range rk {
			rk[j] ^= kb[128+j]
		}
		l[i] = refEnc(l[i], rk)
	}
	l[0] = refEnc(l[0], l[1])
	l[2] = refEnc(l[2], l[3])
	l[4] = refEnc(l[4], l[5])
	l[6] = refEnc(l[6], l[7])
	l[0] = refEnc(l[0], l[2])
	l[4] = refEnc(l[4], l[6])
	x := refEnc(l[0], l[4])
	binary.LittleEndian.PutUint64(x[:], binary.LittleEndian.Uint64(x[:])^uint64(len(b)))
	x = refEnc(x, [16]byte(kb[144:]))
	x = refEnc(x, [16]byte(kb[160:]))
	x = refEnc(x, [16]byte(kb[144:]))
	return binary.LittleEndian.Uint64(x[:])
}

// TestRefAES checks refRound, through AES-128 built from it, against the
// example of FIPS 197, appendix C.1, and against crypto/aes.
func TestRefAES(t *testing.T) {
	var key, in [16]byte
	// The key and block of the example: bytes 0x00, 0x01, ... and 0x00,
	// 0x11, ...
	for i := range key {
		key[i], in[i] = byte(i), byte(i*0x11)
	}
	if got := refAES128(key, in); hex.EncodeToString(got[:]) != "69c4e0d86a7b0430d8cdb78070b4c55a" {
		t.Fatalf("FIPS 197 C.1: got %x", got)
	}
	r := rand.New(rand.NewSource(5))
	for range 200 {
		r.Read(key[:])
		r.Read(in[:])
		c, err := aes.NewCipher(key[:])
		if err != nil {
			t.Fatal(err)
		}
		var want [16]byte
		c.Encrypt(want[:], in[:])
		if got := refAES128(key, in); !bytes.Equal(got[:], want[:]) {
			t.Fatalf("key %x, block %x: got %x, crypto/aes gives %x", key, in, got, want)
		}
	}
}

// TestHashLimitCPU checks Sum, with each kernel LimitCPU leaves it: from
// 128 bytes on it is refHashAES where FastSum holds, and maphash.Bytes
// elsewhere, as it is below 128 bytes.
func TestHashLimitCPU(t *testing.T) {
	h := NewHashKey()
	r := rand.New(rand.NewSource(13))
	buf := make([]byte, 5000)
	r.Read(buf)
	for _, drop := range []string{"", "avx2", "avx", "aes", "aes,avx"} {
		undo := LimitCPU(drop)
		for _, n := range []int{0, 1, 127, 128, 129, 255, 256, 1000, 4096, 4097, 5000} {
			b := buf[:n]
			want := maphash.Bytes(h.seed, b)
			if FastSum() && n >= 128 {
				want = refHashAES(&h.k, b)
			}
			if got := h.Sum(b); got != want {
				undo()
				t.Fatalf("LimitCPU(%q), n %d: Sum gives %#x, want %#x", drop, n, got, want)
			}
		}
		undo()
	}
}
