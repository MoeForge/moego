//go:build amd64 && !purego

#include "textflag.h"

TEXT ·supportsAVX2(SB), NOSPLIT, $0-1
    MOVB $0, ret+0(FP)
    XORL AX, AX
    CPUID
    CMPL AX, $7
    JB avx2_unsupported
    MOVL $1, AX
    CPUID
    ANDL $0x1c000000, CX
    CMPL CX, $0x1c000000
    JNE avx2_unsupported
    XORL CX, CX
    XGETBV
    ANDL $6, AX
    CMPL AX, $6
    JNE avx2_unsupported
    MOVL $7, AX
    XORL CX, CX
    CPUID
    TESTL $32, BX
    JZ avx2_unsupported
    MOVB $1, ret+0(FP)
avx2_unsupported:
    RET

// ENCODE turns the reshuffled bytes in each dword into four sextets, then
// adds per-class offsets: 0=A-Z, 1=a-z, 2..11=0-9, 12/13=symbols.
#define ENCODE(in, t0, t1) \
    VPAND Y9, in, t0; \
    VPMULHUW Y10, t0, t0; \
    VPAND Y11, in, in; \
    VPMULLW Y12, in, in; \
    VPOR t0, in, in; \
    VPSUBUSB encode51<>(SB), in, t0; \
    VPCMPGTB encode25<>(SB), in, t1; \
    VPSUBB t1, t0, t0; \
    VPSHUFB t0, Y13, t0; \
    VPADDB t0, in, in

// ENCODE_A loads two blocks and starts the reshuffle multiplies: lo gets
// the low-mask product and hi the high-mask product.
#define ENCODE_A(off0, off1, lo0, lo1, hi0, hi1) \
    VMOVDQU off0(SI), lo0; \
    VMOVDQU off1(SI), lo1; \
    VPSHUFB Y8, lo0, lo0; \
    VPSHUFB Y8, lo1, lo1; \
    VPAND Y9, lo0, hi0; \
    VPAND Y9, lo1, hi1; \
    VPAND Y11, lo0, lo0; \
    VPAND Y11, lo1, lo1; \
    VPMULHUW Y10, hi0, hi0; \
    VPMULHUW Y10, hi1, hi1; \
    VPMULLW Y12, lo0, lo0; \
    VPMULLW Y12, lo1, lo1

// ENCODE_B finishes two blocks from ENCODE_A and stores them.
#define ENCODE_B(lo0, lo1, hi0, hi1, doff0, doff1) \
    VPOR hi0, lo0, lo0; \
    VPOR hi1, lo1, lo1; \
    VPSUBUSB encode51<>(SB), lo0, Y14; \
    VPCMPGTB encode25<>(SB), lo0, Y15; \
    VPSUBB Y15, Y14, Y14; \
    VPSHUFB Y14, Y13, Y14; \
    VPADDB Y14, lo0, lo0; \
    VPSUBUSB encode51<>(SB), lo1, Y14; \
    VPCMPGTB encode25<>(SB), lo1, Y15; \
    VPSUBB Y15, Y14, Y14; \
    VPSHUFB Y14, Y13, Y14; \
    VPADDB Y14, lo1, lo1; \
    VMOVDQU lo0, doff0(DI); \
    VMOVDQU lo1, doff1(DI)

// ENCODE_EXACT encodes the 24 bytes at SI to DI with two loads that stay
// inside them. VINSERTI128 with a memory operand does not use the shuffle port.
#define ENCODE_EXACT \
    VMOVDQU (SI), X0; \
    VINSERTI128 $1, 8(SI), Y0, Y0; \
    VPSHUFB encodeShuffle<>(SB), Y0, Y0; \
    ENCODE(Y0, Y4, Y5); \
    VMOVDQU Y0, (DI)

// encodeAVX2 encodes whole 3-byte groups when at least 24 source bytes fit in
// dst. It returns the source bytes consumed, always a multiple of three.
TEXT ·encodeAVX2(SB), NOSPLIT, $0-64
    MOVQ dst_base+0(FP), DI
    MOVQ dst_len+8(FP), AX
    MOVQ src_base+24(FP), SI
    MOVQ src_len+32(FP), CX
    MOVQ SI, R9
    SHRQ $2, AX
    LEAQ (AX)(AX*2), AX
    CMPQ AX, CX
    CMOVQLT AX, CX
    MOVQ $0xaaaaaaaaaaaaaaab, AX
    MULQ CX
    SHRQ $1, DX
    LEAQ (DX)(DX*2), CX
    CMPQ CX, $24
    JB encode_empty
    LEAQ (SI)(CX*1), R11
    MOVQ alphabet+48(FP), AX
    LEAQ encodeOffsetsStd<>(SB), DX
    CMPB 63(AX), $'_'
    JNE encode_tables
    LEAQ encodeOffsetsURL<>(SB), DX
encode_tables:
    VMOVDQU encodeShuffleShifted<>(SB), Y8
    VMOVDQU encodeMaskHigh<>(SB), Y9
    VMOVDQU encodeMultiplyHigh<>(SB), Y10
    VMOVDQU encodeMaskLow<>(SB), Y11
    VMOVDQU encodeMultiplyLow<>(SB), Y12
    VMOVDQU (DX), Y13
    // The first block cannot read before src.
    ENCODE_EXACT
    ADDQ $24, SI
    ADDQ $32, DI
    // Later loads start four bytes early, so one 32-byte load and one shuffle
    // cover a block. Each load also reads four bytes past its block. The loop
    // is software-pipelined: the multiplies for the next two blocks overlap
    // the translation of the current two. Prefetching the source ahead of
    // the hardware streamer measurably helps inputs larger than L2.
    LEAQ -148(R11), R10
    CMPQ SI, R10
    JA encode_single
    ENCODE_A(-4, 20, Y0, Y1, Y2, Y3)
    PCALIGN $32
encode_loop4:
    PREFETCHT0 1024(SI)
    ENCODE_A(44, 68, Y4, Y5, Y6, Y7)
    ENCODE_B(Y0, Y1, Y2, Y3, 0, 32)
    ENCODE_A(92, 116, Y0, Y1, Y2, Y3)
    ENCODE_B(Y4, Y5, Y6, Y7, 64, 96)
    ADDQ $96, SI
    ADDQ $128, DI
    CMPQ SI, R10
    JBE encode_loop4
    ENCODE_B(Y0, Y1, Y2, Y3, 0, 32)
    ADDQ $48, SI
    ADDQ $64, DI
encode_single:
    LEAQ -28(R11), R10
    CMPQ SI, R10
    JA encode_last
encode_loop1:
    VMOVDQU -4(SI), Y0
    VPSHUFB Y8, Y0, Y0
    ENCODE(Y0, Y4, Y5)
    VMOVDQU Y0, (DI)
    ADDQ $24, SI
    ADDQ $32, DI
    CMPQ SI, R10
    JBE encode_loop1
encode_last:
    // At most 27 group bytes remain. Read them without crossing R11.
    MOVQ R11, AX
    SUBQ SI, AX
    CMPQ AX, $24
    JB encode_window
    ENCODE_EXACT
    ADDQ $24, SI
    ADDQ $32, DI
    SUBQ $24, AX
encode_window:
    TESTQ AX, AX
    JZ encode_done
    // A full block ending at R11 rewrites earlier output with equal bytes.
    IMUL3Q $0x56, AX, AX
    SHRQ $8, AX
    LEAQ -32(DI)(AX*4), DI
    LEAQ -24(R11), SI
    ENCODE_EXACT
    MOVQ R11, SI
encode_done:
    VZEROUPPER
encode_empty:
    SUBQ R9, SI
    MOVQ SI, ret+56(FP)
    RET

// DECODE_HIGH stores each byte's high nibble.
#define DECODE_HIGH(in, hi) \
    VPSRLD $4, in, hi; \
    VPAND Y15, hi, hi

// DECODE_VALID sets nonzero bytes in out for alphabet characters. The low
// lookup uses the raw byte, so bytes with bit 7 set produce zero.
#define DECODE_VALID(in, hi, out, tmp) \
    VPSHUFB in, Y14, out; \
    VPSHUFB hi, Y13, tmp; \
    VPAND tmp, out, out

// DECODE_PACK maps characters to sextets and packs each 128-bit lane into
// twelve bytes followed by four zero bytes. The roll index is the high nibble,
// plus one above the per-alphabet split point at 64(R8).
#define DECODE_PACK(in, hi) \
    VPCMPGTB 64(R8), in, Y9; \
    VPSUBB Y9, hi, hi; \
    VPSHUFB hi, Y12, hi; \
    VPADDB hi, in, in; \
    VPMADDUBSW decodePairMultiply<>(SB), in, in; \
    VPMADDWD decodeQuadMultiply<>(SB), in, in; \
    VPSHUFB decodePackShuffle<>(SB), in, in

// DECODE_STORE24 writes exactly the 24 decoded bytes in Y0 to the address in
// reg: lane 0 receives bytes 0..15 and lane 1 receives bytes 8..23.
#define DECODE_STORE24(reg) \
    VMOVDQU decodeStorePermute<>(SB), Y10; \
    VPERMD Y0, Y10, Y0; \
    VMOVDQU X0, (reg); \
    VEXTRACTI128 $1, Y0, 8(reg)

// DECODE_ONE validates the block in Y0 and jumps to fail before any store.
#define DECODE_ONE(fail) \
    DECODE_HIGH(Y0, Y4); \
    DECODE_VALID(Y0, Y4, Y8, Y9); \
    VPXOR Y9, Y9, Y9; \
    VPCMPEQB Y9, Y8, Y8; \
    VPMOVMSKB Y8, AX; \
    TESTL AX, AX; \
    JNZ fail; \
    DECODE_PACK(Y0, Y4)

// DECODE_A loads two blocks at off(SI), jumps to fail unless both contain
// only alphabet characters, and leaves their sextets in s0 and s1. Y11 holds
// the alphabet's split point.
#define DECODE_A(off, s0, s1, fail) \
    VMOVDQU off(SI), s0; \
    VMOVDQU 32+off(SI), s1; \
    VPSRLD $4, s0, Y4; \
    VPSRLD $4, s1, Y5; \
    VPAND Y15, Y4, Y4; \
    VPAND Y15, Y5, Y5; \
    VPSHUFB s0, Y14, Y6; \
    VPSHUFB s1, Y14, Y7; \
    VPSHUFB Y4, Y13, Y8; \
    VPSHUFB Y5, Y13, Y9; \
    VPAND Y8, Y6, Y6; \
    VPAND Y9, Y7, Y7; \
    VPMINUB Y7, Y6, Y6; \
    VPXOR Y7, Y7, Y7; \
    VPCMPEQB Y7, Y6, Y6; \
    VPMOVMSKB Y6, AX; \
    TESTL AX, AX; \
    JNZ fail; \
    VPCMPGTB Y11, s0, Y6; \
    VPCMPGTB Y11, s1, Y7; \
    VPSUBB Y6, Y4, Y4; \
    VPSUBB Y7, Y5, Y5; \
    VPSHUFB Y4, Y12, Y4; \
    VPSHUFB Y5, Y12, Y5; \
    VPADDB Y4, s0, s0; \
    VPADDB Y5, s1, s1

// DECODE_B packs the sextets of two blocks into twelve bytes per lane.
#define DECODE_B(s0, s1) \
    VPMADDUBSW decodePairMultiply<>(SB), s0, s0; \
    VPMADDUBSW decodePairMultiply<>(SB), s1, s1; \
    VPMADDWD decodeQuadMultiply<>(SB), s0, s0; \
    VPMADDWD decodeQuadMultiply<>(SB), s1, s1; \
    VPSHUFB decodePackShuffle<>(SB), s0, s0; \
    VPSHUFB decodePackShuffle<>(SB), s1, s1

// DECODE_STORE48 stores two packed blocks. Each lane store writes four zero
// bytes after its twelve bytes; the last four land in the next group, which
// must already be validated and will be stored later.
#define DECODE_STORE48(s0, s1, x0, x1, off) \
    VMOVDQU x0, off(DI); \
    VEXTRACTI128 $1, s0, 12+off(DI); \
    VMOVDQU x1, 24+off(DI); \
    VEXTRACTI128 $1, s1, 36+off(DI)

// DECODE_EXACT48 stores two packed blocks without writing past them.
#define DECODE_EXACT48(s0, s1, x0, x1, off) \
    VMOVDQU decodeStorePermute<>(SB), Y10; \
    VPERMD s1, Y10, s1; \
    VMOVDQU x0, off(DI); \
    VEXTRACTI128 $1, s0, 12+off(DI); \
    VMOVDQU x1, 24+off(DI); \
    VEXTRACTI128 $1, s1, 32+off(DI)

// decodeAVX2 decodes 32-byte blocks while dst has room. It stops before any
// block containing padding, whitespace or invalid input. When fewer than 32
// bytes remain and they are whole quanta, it decodes the last 32 bytes of src
// and rewrites the overlapping output with equal bytes. It never stores past
// the returned prefix.
TEXT ·decodeAVX2(SB), NOSPLIT, $0-64
    MOVQ dst_base+0(FP), DI
    MOVQ dst_len+8(FP), R8
    MOVQ src_base+24(FP), SI
    MOVQ src_len+32(FP), CX
    MOVQ SI, R9
    LEAQ (SI)(CX*1), R10
    LEAQ (DI)(R8*1), R11
    CMPQ CX, $32
    JB decode_empty
    // R8 points at the alphabet's parameters from here on.
    LEAQ decodeParamsStd<>(SB), R8
    CMPB url+48(FP), $0
    JE decode_tables
    LEAQ decodeParamsURL<>(SB), R8
decode_tables:
    VMOVDQU decodeNibbleMask<>(SB), Y15
    VMOVDQU (R8), Y14
    VMOVDQU decodeHighBits<>(SB), Y13
    VMOVDQU 32(R8), Y12
    // The loop is software-pipelined over two-block groups: it validates
    // and translates the next group while packing and storing the current
    // one. At the top of the loop, Y0/Y1 hold the validated group at SI.
    // Prefetching the source ahead of the hardware streamer measurably helps
    // inputs larger than L2.
    VMOVDQU 64(R8), Y11
    LEAQ -192(R10), R12
    LEAQ -144(R11), R13
    CMPQ SI, R12
    JA decode_single
    CMPQ DI, R13
    JA decode_single
    DECODE_A(0, Y0, Y1, decode_single)
    PCALIGN $32
decode_loop4:
    PREFETCHT0 1024(SI)
    PREFETCHT0 1088(SI)
    DECODE_A(64, Y2, Y3, decode_drain0)
    DECODE_B(Y0, Y1)
    DECODE_STORE48(Y0, Y1, X0, X1, 0)
    DECODE_A(128, Y0, Y1, decode_drain1)
    DECODE_B(Y2, Y3)
    DECODE_STORE48(Y2, Y3, X2, X3, 48)
    ADDQ $128, SI
    ADDQ $96, DI
    CMPQ SI, R12
    JA decode_drain0
    CMPQ DI, R13
    JBE decode_loop4
decode_drain0:
    // Y0/Y1 hold the validated group at SI.
    DECODE_B(Y0, Y1)
    DECODE_EXACT48(Y0, Y1, X0, X1, 0)
    ADDQ $64, SI
    ADDQ $48, DI
    JMP decode_single
decode_drain1:
    // The group at SI is stored; Y2/Y3 hold the validated group after it.
    DECODE_B(Y2, Y3)
    DECODE_EXACT48(Y2, Y3, X2, X3, 48)
    ADDQ $128, SI
    ADDQ $96, DI
decode_single:
    LEAQ -32(R10), R12
    LEAQ -24(R11), R13
decode_loop1:
    CMPQ SI, R12
    JA decode_window
    CMPQ DI, R13
    JA decode_window
    VMOVDQU (SI), Y0
    DECODE_ONE(decode_done)
    DECODE_STORE24(DI)
    ADDQ $32, SI
    ADDQ $24, DI
    JMP decode_loop1
decode_window:
    MOVQ R10, CX
    SUBQ SI, CX
    JZ decode_done
    CMPQ CX, $32
    JAE decode_done
    TESTQ $3, CX
    JNZ decode_done
    MOVQ CX, DX
    SHRQ $2, DX
    LEAQ (DX)(DX*2), DX
    ADDQ DI, DX
    CMPQ DX, R11
    JA decode_done
    VMOVDQU -32(R10), Y0
    DECODE_ONE(decode_done)
    LEAQ -24(DX), BX
    DECODE_STORE24(BX)
    MOVQ R10, SI
decode_done:
    VZEROUPPER
decode_empty:
    SUBQ R9, SI
    MOVQ SI, consumed+56(FP)
    RET

// validAVX2 returns the longest prefix of whole 64-byte blocks that contains
// only alphabet characters. It performs the decoder's checks without output.
TEXT ·validAVX2(SB), NOSPLIT, $0-40
    MOVQ src_base+0(FP), SI
    MOVQ src_len+8(FP), CX
    MOVQ SI, R9
    LEAQ (SI)(CX*1), R10
    CMPQ CX, $64
    JB valid_empty
    LEAQ decodeParamsStd<>(SB), AX
    CMPB url+24(FP), $0
    JE valid_tables
    LEAQ decodeParamsURL<>(SB), AX
valid_tables:
    VMOVDQU decodeNibbleMask<>(SB), Y15
    VMOVDQU (AX), Y14
    VMOVDQU decodeHighBits<>(SB), Y13
    VPXOR Y12, Y12, Y12
    LEAQ -128(R10), R12
    CMPQ SI, R12
    JA valid_pair
    PCALIGN $32
valid_loop4:
    PREFETCHT0 1024(SI)
    PREFETCHT0 1088(SI)
    VMOVDQU (SI), Y0
    VMOVDQU 32(SI), Y1
    VMOVDQU 64(SI), Y2
    VMOVDQU 96(SI), Y3
    DECODE_HIGH(Y0, Y4)
    DECODE_HIGH(Y1, Y5)
    DECODE_HIGH(Y2, Y6)
    DECODE_HIGH(Y3, Y7)
    DECODE_VALID(Y0, Y4, Y8, Y9)
    DECODE_VALID(Y1, Y5, Y10, Y11)
    DECODE_VALID(Y2, Y6, Y4, Y5)
    DECODE_VALID(Y3, Y7, Y6, Y7)
    VPMINUB Y10, Y8, Y8
    VPMINUB Y6, Y4, Y4
    VPMINUB Y4, Y8, Y8
    VPCMPEQB Y12, Y8, Y8
    VPMOVMSKB Y8, AX
    TESTL AX, AX
    JNZ valid_pair
    ADDQ $128, SI
    CMPQ SI, R12
    JBE valid_loop4
valid_pair:
    LEAQ -64(R10), R12
valid_loop2:
    CMPQ SI, R12
    JA valid_done
    VMOVDQU (SI), Y0
    VMOVDQU 32(SI), Y1
    DECODE_HIGH(Y0, Y4)
    DECODE_HIGH(Y1, Y5)
    DECODE_VALID(Y0, Y4, Y8, Y9)
    DECODE_VALID(Y1, Y5, Y10, Y11)
    VPMINUB Y10, Y8, Y8
    VPCMPEQB Y12, Y8, Y8
    VPMOVMSKB Y8, AX
    TESTL AX, AX
    JNZ valid_done
    ADDQ $64, SI
    JMP valid_loop2
valid_done:
    VZEROUPPER
valid_empty:
    SUBQ R9, SI
    MOVQ SI, ret+32(FP)
    RET

// Lane 0 starts at source byte 0 and lane 1 at source byte 8.
DATA encodeShuffle<>+0(SB)/8, $0x0405030401020001
DATA encodeShuffle<>+8(SB)/8, $0x0a0b090a07080607
DATA encodeShuffle<>+16(SB)/8, $0x0809070805060405
DATA encodeShuffle<>+24(SB)/8, $0x0e0f0d0e0b0c0a0b
GLOBL encodeShuffle<>(SB), RODATA|NOPTR, $32

// The load starts four bytes before the block, so the lane offsets swap.
DATA encodeShuffleShifted<>+0(SB)/8, $0x0809070805060405
DATA encodeShuffleShifted<>+8(SB)/8, $0x0e0f0d0e0b0c0a0b
DATA encodeShuffleShifted<>+16(SB)/8, $0x0405030401020001
DATA encodeShuffleShifted<>+24(SB)/8, $0x0a0b090a07080607
GLOBL encodeShuffleShifted<>(SB), RODATA|NOPTR, $32

DATA encodeMaskHigh<>+0(SB)/8, $0x0fc0fc000fc0fc00
DATA encodeMaskHigh<>+8(SB)/8, $0x0fc0fc000fc0fc00
DATA encodeMaskHigh<>+16(SB)/8, $0x0fc0fc000fc0fc00
DATA encodeMaskHigh<>+24(SB)/8, $0x0fc0fc000fc0fc00
GLOBL encodeMaskHigh<>(SB), RODATA|NOPTR, $32

DATA encodeMultiplyHigh<>+0(SB)/8, $0x0400004004000040
DATA encodeMultiplyHigh<>+8(SB)/8, $0x0400004004000040
DATA encodeMultiplyHigh<>+16(SB)/8, $0x0400004004000040
DATA encodeMultiplyHigh<>+24(SB)/8, $0x0400004004000040
GLOBL encodeMultiplyHigh<>(SB), RODATA|NOPTR, $32

DATA encodeMaskLow<>+0(SB)/8, $0x003f03f0003f03f0
DATA encodeMaskLow<>+8(SB)/8, $0x003f03f0003f03f0
DATA encodeMaskLow<>+16(SB)/8, $0x003f03f0003f03f0
DATA encodeMaskLow<>+24(SB)/8, $0x003f03f0003f03f0
GLOBL encodeMaskLow<>(SB), RODATA|NOPTR, $32

DATA encodeMultiplyLow<>+0(SB)/8, $0x0100001001000010
DATA encodeMultiplyLow<>+8(SB)/8, $0x0100001001000010
DATA encodeMultiplyLow<>+16(SB)/8, $0x0100001001000010
DATA encodeMultiplyLow<>+24(SB)/8, $0x0100001001000010
GLOBL encodeMultiplyLow<>(SB), RODATA|NOPTR, $32

DATA encodeOffsetsStd<>+0(SB)/8, $0xfcfcfcfcfcfc4741
DATA encodeOffsetsStd<>+8(SB)/8, $0x0000f0edfcfcfcfc
DATA encodeOffsetsStd<>+16(SB)/8, $0xfcfcfcfcfcfc4741
DATA encodeOffsetsStd<>+24(SB)/8, $0x0000f0edfcfcfcfc
GLOBL encodeOffsetsStd<>(SB), RODATA|NOPTR, $32

DATA encodeOffsetsURL<>+0(SB)/8, $0xfcfcfcfcfcfc4741
DATA encodeOffsetsURL<>+8(SB)/8, $0x000020effcfcfcfc
DATA encodeOffsetsURL<>+16(SB)/8, $0xfcfcfcfcfcfc4741
DATA encodeOffsetsURL<>+24(SB)/8, $0x000020effcfcfcfc
GLOBL encodeOffsetsURL<>(SB), RODATA|NOPTR, $32

DATA encode51<>+0(SB)/8, $0x3333333333333333
DATA encode51<>+8(SB)/8, $0x3333333333333333
DATA encode51<>+16(SB)/8, $0x3333333333333333
DATA encode51<>+24(SB)/8, $0x3333333333333333
GLOBL encode51<>(SB), RODATA|NOPTR, $32

DATA encode25<>+0(SB)/8, $0x1919191919191919
DATA encode25<>+8(SB)/8, $0x1919191919191919
DATA encode25<>+16(SB)/8, $0x1919191919191919
DATA encode25<>+24(SB)/8, $0x1919191919191919
GLOBL encode25<>(SB), RODATA|NOPTR, $32

DATA decodeNibbleMask<>+0(SB)/8, $0x0f0f0f0f0f0f0f0f
DATA decodeNibbleMask<>+8(SB)/8, $0x0f0f0f0f0f0f0f0f
DATA decodeNibbleMask<>+16(SB)/8, $0x0f0f0f0f0f0f0f0f
DATA decodeNibbleMask<>+24(SB)/8, $0x0f0f0f0f0f0f0f0f
GLOBL decodeNibbleMask<>(SB), RODATA|NOPTR, $32

// Bit h is set for high nibble h < 8. Bytes from 0x80 have no bit.
DATA decodeHighBits<>+0(SB)/8, $0x8040201008040201
DATA decodeHighBits<>+8(SB)/8, $0x0000000000000000
DATA decodeHighBits<>+16(SB)/8, $0x8040201008040201
DATA decodeHighBits<>+24(SB)/8, $0x0000000000000000
GLOBL decodeHighBits<>(SB), RODATA|NOPTR, $32

// Per alphabet: low-nibble masks of valid high nibbles, roll offsets indexed
// by high nibble (+1 above the split point), and the split point. Standard
// splits '+' from '/' at 0x2e; URL splits 'Z' from '_' at 0x5e.
DATA decodeParamsStd<>+0(SB)/8, $0xf8f8f8f8f8f8f8a8
DATA decodeParamsStd<>+8(SB)/8, $0x5450505054f0f8f8
DATA decodeParamsStd<>+16(SB)/8, $0xf8f8f8f8f8f8f8a8
DATA decodeParamsStd<>+24(SB)/8, $0x5450505054f0f8f8
DATA decodeParamsStd<>+32(SB)/8, $0xb9bfbf0410130000
DATA decodeParamsStd<>+40(SB)/8, $0x00000000000000b9
DATA decodeParamsStd<>+48(SB)/8, $0xb9bfbf0410130000
DATA decodeParamsStd<>+56(SB)/8, $0x00000000000000b9
DATA decodeParamsStd<>+64(SB)/8, $0x2e2e2e2e2e2e2e2e
DATA decodeParamsStd<>+72(SB)/8, $0x2e2e2e2e2e2e2e2e
DATA decodeParamsStd<>+80(SB)/8, $0x2e2e2e2e2e2e2e2e
DATA decodeParamsStd<>+88(SB)/8, $0x2e2e2e2e2e2e2e2e
GLOBL decodeParamsStd<>(SB), RODATA|NOPTR, $96

DATA decodeParamsURL<>+0(SB)/8, $0xf8f8f8f8f8f8f8a8
DATA decodeParamsURL<>+8(SB)/8, $0x7050545050f0f8f8
DATA decodeParamsURL<>+16(SB)/8, $0xf8f8f8f8f8f8f8a8
DATA decodeParamsURL<>+24(SB)/8, $0x7050545050f0f8f8
DATA decodeParamsURL<>+32(SB)/8, $0xb9e0bfbf04110000
DATA decodeParamsURL<>+40(SB)/8, $0x00000000000000b9
DATA decodeParamsURL<>+48(SB)/8, $0xb9e0bfbf04110000
DATA decodeParamsURL<>+56(SB)/8, $0x00000000000000b9
DATA decodeParamsURL<>+64(SB)/8, $0x5e5e5e5e5e5e5e5e
DATA decodeParamsURL<>+72(SB)/8, $0x5e5e5e5e5e5e5e5e
DATA decodeParamsURL<>+80(SB)/8, $0x5e5e5e5e5e5e5e5e
DATA decodeParamsURL<>+88(SB)/8, $0x5e5e5e5e5e5e5e5e
GLOBL decodeParamsURL<>(SB), RODATA|NOPTR, $96

DATA decodePairMultiply<>+0(SB)/8, $0x0140014001400140
DATA decodePairMultiply<>+8(SB)/8, $0x0140014001400140
DATA decodePairMultiply<>+16(SB)/8, $0x0140014001400140
DATA decodePairMultiply<>+24(SB)/8, $0x0140014001400140
GLOBL decodePairMultiply<>(SB), RODATA|NOPTR, $32

DATA decodeQuadMultiply<>+0(SB)/8, $0x0001100000011000
DATA decodeQuadMultiply<>+8(SB)/8, $0x0001100000011000
DATA decodeQuadMultiply<>+16(SB)/8, $0x0001100000011000
DATA decodeQuadMultiply<>+24(SB)/8, $0x0001100000011000
GLOBL decodeQuadMultiply<>(SB), RODATA|NOPTR, $32

DATA decodePackShuffle<>+0(SB)/8, $0x090a040506000102
DATA decodePackShuffle<>+8(SB)/8, $0x808080800c0d0e08
DATA decodePackShuffle<>+16(SB)/8, $0x090a040506000102
DATA decodePackShuffle<>+24(SB)/8, $0x808080800c0d0e08
GLOBL decodePackShuffle<>(SB), RODATA|NOPTR, $32

// Lane 0 receives output bytes 0..15 and lane 1 receives bytes 8..23.
DATA decodeStorePermute<>+0(SB)/8, $0x0000000100000000
DATA decodeStorePermute<>+8(SB)/8, $0x0000000400000002
DATA decodeStorePermute<>+16(SB)/8, $0x0000000400000002
DATA decodeStorePermute<>+24(SB)/8, $0x0000000600000005
GLOBL decodeStorePermute<>(SB), RODATA|NOPTR, $32
