//go:build amd64 && !purego

#include "textflag.h"

// func supportsAVX2() bool
TEXT ·supportsAVX2(SB), NOSPLIT, $0-1
	MOVB $0, ret+0(FP)
	XORL AX, AX
	CPUID
	CMPL AX, $7
	JB   no
	MOVL $1, AX
	CPUID
	ANDL $0x1c000000, CX
	CMPL CX, $0x1c000000
	JNE  no
	XORL CX, CX
	XGETBV
	ANDL $6, AX
	CMPL AX, $6
	JNE  no
	MOVL $7, AX
	XORL CX, CX
	CPUID
	TESTL $32, BX
	JZ   no
	MOVB $1, ret+0(FP)
no:
	RET

// EQ loads 32 bytes, ORs in the mask and compares with the needle byte.
#define EQ(src, mask, needle, dst) \
	VMOVDQU src, dst; \
	VPOR    mask, dst, dst; \
	VPCMPEQB needle, dst, dst

// func pairAVX2(a, b *byte, n int, r1, r2, m1, m2 byte) int
//
// Returns the smallest i < n with a[i]|m1 == r1 and b[i]|m2 == r2, or -1.
// n >= 32. The rarer byte is a: b is only loaded where a matched.
TEXT ·pairAVX2(SB), NOSPLIT, $0-40
	MOVQ a+0(FP), SI
	MOVQ b+8(FP), DI
	MOVQ n+16(FP), CX
	// Broadcast from memory: a legacy-SSE MOVQ into an XMM register while
	// the upper YMM halves are dirty costs a state transition on
	// Haswell/Broadwell.
	VPBROADCASTB r1+24(FP), Y0
	VPBROADCASTB r2+25(FP), Y1
	VPBROADCASTB m1+26(FP), Y2
	VPBROADCASTB m2+27(FP), Y3
	XORQ BX, BX

	// 64 positions per iteration while they fit.
	MOVQ CX, R8
	SUBQ $64, R8
	JB   loop32

loop64:
	EQ((SI)(BX*1), Y2, Y0, Y4)
	EQ(32(SI)(BX*1), Y2, Y0, Y5)
	VPOR   Y4, Y5, Y6
	VPTEST Y6, Y6
	JNZ  check64
next64:
	ADDQ $64, BX
	CMPQ BX, R8
	JBE  loop64
	JMP  loop32

check64:
	EQ((DI)(BX*1), Y3, Y1, Y6)
	EQ(32(DI)(BX*1), Y3, Y1, Y7)
	VPAND Y6, Y4, Y4
	VPAND Y7, Y5, Y5
	VPMOVMSKB Y4, AX
	TESTL AX, AX
	JNZ  found
	VPMOVMSKB Y5, AX
	TESTL AX, AX
	JZ   next64
	ADDQ $32, BX
	JMP  found

loop32:
	MOVQ CX, R8
	SUBQ $32, R8
loop32body:
	CMPQ BX, R8
	JA   tail
	EQ((SI)(BX*1), Y2, Y0, Y4)
	EQ((DI)(BX*1), Y3, Y1, Y5)
	VPAND Y5, Y4, Y4
	VPMOVMSKB Y4, AX
	TESTL AX, AX
	JNZ  found
	ADDQ $32, BX
	JMP  loop32body

tail:
	// Fewer than 32 positions remain: test the last 32 again and drop
	// the lanes already tested.
	CMPQ BX, CX
	JAE  notfound
	MOVQ BX, R9
	EQ((SI)(R8*1), Y2, Y0, Y4)
	EQ((DI)(R8*1), Y3, Y1, Y5)
	VPAND Y5, Y4, Y4
	VPMOVMSKB Y4, AX
	MOVQ R9, CX
	SUBQ R8, CX
	SHRL CX, AX
	TESTL AX, AX
	JZ   notfound
	MOVQ R9, BX

found:
	BSFL AX, AX
	ADDQ BX, AX
	MOVQ AX, ret+32(FP)
	VZEROUPPER
	RET

notfound:
	MOVQ $-1, ret+32(FP)
	VZEROUPPER
	RET

// CLASS sets dst to 0xFF in lanes whose byte is not a class member.
// Membership of b is lo[b&15] & hi[b>>4] != 0; hi is zero for b >= 0x80.
#define CLASS(src, dst, tmp) \
	VPAND   Y3, src, dst; \
	VPSHUFB dst, Y0, dst; \
	VPSRLW  $4, src, tmp; \
	VPAND   Y3, tmp, tmp; \
	VPSHUFB tmp, Y1, tmp; \
	VPAND   tmp, dst, dst; \
	VPCMPEQB Y8, dst, dst

// func spanAVX2(p *byte, n int, lo, hi *[16]byte, inv uint32) int
//
// Returns the first i < n with a non-member byte (inv = 0) or a member
// byte (inv = 0xFFFFFFFF), or n. n >= 32.
TEXT ·spanAVX2(SB), NOSPLIT, $0-48
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	MOVQ lo+16(FP), AX
	MOVQ hi+24(FP), DX
	MOVL inv+32(FP), R10
	VBROADCASTI128 (AX), Y0
	VBROADCASTI128 (DX), Y1
	VPCMPEQB Y3, Y3, Y3
	VPSRLW   $12, Y3, Y3
	VPACKUSWB Y3, Y3, Y3     // 0x0F in every byte
	VPXOR    Y8, Y8, Y8
	XORQ BX, BX
	MOVQ CX, R8
	SUBQ $64, R8
	JB   span32

span64:
	VMOVDQU (SI)(BX*1), Y4
	VMOVDQU 32(SI)(BX*1), Y5
	CLASS(Y4, Y6, Y7)
	CLASS(Y5, Y9, Y7)
	VPMOVMSKB Y6, AX
	VPMOVMSKB Y9, DX
	XORL R10, AX
	XORL R10, DX
	SHLQ $32, DX
	ORQ  DX, AX
	JNZ  spanfound
	ADDQ $64, BX
	CMPQ BX, R8
	JBE  span64

span32:
	MOVQ CX, R8
	SUBQ $32, R8
span32body:
	CMPQ BX, R8
	JA   spantail
	VMOVDQU (SI)(BX*1), Y4
	CLASS(Y4, Y6, Y7)
	VPMOVMSKB Y6, AX
	XORL R10, AX
	JNZ  spanfound
	ADDQ $32, BX
	JMP  span32body

spantail:
	CMPQ BX, CX
	JAE  spanend
	// Re-test the last 32 bytes and drop the lanes already tested.
	VMOVDQU (SI)(R8*1), Y4
	CLASS(Y4, Y6, Y7)
	VPMOVMSKB Y6, AX
	XORL R10, AX
	MOVQ BX, R9
	MOVQ BX, CX
	SUBQ R8, CX
	SHRL CX, AX
	JZ   spannone
	MOVQ R9, BX

spanfound:
	BSFQ AX, AX
	ADDQ BX, AX
	MOVQ AX, ret+40(FP)
	VZEROUPPER
	RET

spannone:
	MOVQ n+8(FP), CX
spanend:
	MOVQ CX, ret+40(FP)
	VZEROUPPER
	RET

// func classMasksAVX2(p *byte, n int, lo, hi *[16]byte, out *uint64)
//
// Sets out[k] to the membership bits of p[64k : 64k+64] for each k with
// 64k+64 <= n.
TEXT ·classMasksAVX2(SB), NOSPLIT, $0-40
	MOVQ p+0(FP), SI
	MOVQ n+8(FP), CX
	MOVQ lo+16(FP), AX
	MOVQ hi+24(FP), DX
	MOVQ out+32(FP), DI
	VBROADCASTI128 (AX), Y0
	VBROADCASTI128 (DX), Y1
	VPCMPEQB Y3, Y3, Y3
	VPSRLW   $12, Y3, Y3
	VPACKUSWB Y3, Y3, Y3     // 0x0F in every byte
	VPXOR    Y8, Y8, Y8
	SHRQ $6, CX
	JZ   masksdone

masksloop:
	VMOVDQU (SI), Y4
	VMOVDQU 32(SI), Y5
	CLASS(Y4, Y6, Y7)
	CLASS(Y5, Y9, Y7)
	VPMOVMSKB Y6, AX
	VPMOVMSKB Y9, DX
	SHLQ $32, DX
	ORQ  DX, AX
	NOTQ AX
	MOVQ AX, (DI)
	ADDQ $64, SI
	ADDQ $8, DI
	DECQ CX
	JNZ  masksloop

masksdone:
	VZEROUPPER
	RET
