//go:build amd64 && !purego

#include "textflag.h"

DATA jsonQuotes<>+0(SB)/8, $0x2222222222222222
DATA jsonQuotes<>+8(SB)/8, $0x2222222222222222
DATA jsonQuotes<>+16(SB)/8, $0x2222222222222222
DATA jsonQuotes<>+24(SB)/8, $0x2222222222222222
GLOBL jsonQuotes<>(SB), RODATA|NOPTR, $32

DATA jsonSlashes<>+0(SB)/8, $0x5c5c5c5c5c5c5c5c
DATA jsonSlashes<>+8(SB)/8, $0x5c5c5c5c5c5c5c5c
DATA jsonSlashes<>+16(SB)/8, $0x5c5c5c5c5c5c5c5c
DATA jsonSlashes<>+24(SB)/8, $0x5c5c5c5c5c5c5c5c
GLOBL jsonSlashes<>(SB), RODATA|NOPTR, $32

DATA jsonCase<>+0(SB)/8, $0x2020202020202020
DATA jsonCase<>+8(SB)/8, $0x2020202020202020
DATA jsonCase<>+16(SB)/8, $0x2020202020202020
DATA jsonCase<>+24(SB)/8, $0x2020202020202020
GLOBL jsonCase<>(SB), RODATA|NOPTR, $32

DATA jsonOpens<>+0(SB)/8, $0x7b7b7b7b7b7b7b7b
DATA jsonOpens<>+8(SB)/8, $0x7b7b7b7b7b7b7b7b
DATA jsonOpens<>+16(SB)/8, $0x7b7b7b7b7b7b7b7b
DATA jsonOpens<>+24(SB)/8, $0x7b7b7b7b7b7b7b7b
GLOBL jsonOpens<>(SB), RODATA|NOPTR, $32

DATA jsonCloses<>+0(SB)/8, $0x7d7d7d7d7d7d7d7d
DATA jsonCloses<>+8(SB)/8, $0x7d7d7d7d7d7d7d7d
DATA jsonCloses<>+16(SB)/8, $0x7d7d7d7d7d7d7d7d
DATA jsonCloses<>+24(SB)/8, $0x7d7d7d7d7d7d7d7d
GLOBL jsonCloses<>(SB), RODATA|NOPTR, $32

// BLOCKMASK compares the 64 bytes in a and b with the byte in all lanes of
// c and leaves the 64-bit mask, bit i for byte i, in dst. Clobbers Y2, Y3
// and AX.
#define BLOCKMASK(c, a, b, dst) \
    VPCMPEQB c, a, Y2; \
    VPCMPEQB c, b, Y3; \
    VPMOVMSKB Y2, dst; \
    VPMOVMSKB Y3, AX; \
    SHLQ $32, AX; \
    ORQ AX, dst

// CLASSIFY turns the 64 bytes in Y0 and Y1 into the masks of quotes (BX),
// backslashes (R12), opening brackets (R13) and closing brackets (R14).
// OR 0x20 folds '[' into '{' and ']' into '}', and nothing else into
// either.
#define CLASSIFY \
    BLOCKMASK(Y15, Y0, Y1, BX); \
    BLOCKMASK(Y14, Y0, Y1, R12); \
    VPOR Y13, Y0, Y4; \
    VPOR Y13, Y1, Y5; \
    BLOCKMASK(Y12, Y4, Y5, R13); \
    BLOCKMASK(Y11, Y4, Y5, R14)

// func skipAVX2(p *byte, n int, st *skipState) int
//
// Finds the bracket that closes a container, 64 bytes per step, starting
// at st.pos with the depth, string and escape state in st. It returns the
// index after the closing bracket, -1 when the input ends first, with st
// holding the state at that point, or skipSlow at a backslash outside a
// string.
//
// Quotes not escaped by an odd run of backslashes pair up into strings;
// the bytes inside them come from a carry-less multiply by all ones. '{'
// and '[' outside strings count up, '}' and ']' count down. Blocks inside
// a string that hold no quote or backslash take a shorter loop, so long
// strings such as base64 payloads cost little more than a byte search.
TEXT ·skipAVX2(SB), NOSPLIT, $0-32
    MOVQ p+0(FP), SI
    MOVQ n+8(FP), DX
    MOVQ st+16(FP), DI
    MOVQ 0(DI), R8          // depth
    MOVQ 8(DI), R9          // inStr: 0 or all ones
    MOVQ 16(DI), R10        // esc: 0 or 1
    MOVQ 24(DI), CX         // pos
    VMOVDQU jsonQuotes<>(SB), Y15
    VMOVDQU jsonSlashes<>(SB), Y14
    VMOVDQU jsonCase<>(SB), Y13
    VMOVDQU jsonOpens<>(SB), Y12
    VMOVDQU jsonCloses<>(SB), Y11
    MOVQ $-1, AX
    VMOVQ AX, X10           // all ones, for the prefix XOR

skip_block:
    LEAQ 64(CX), AX
    CMPQ AX, DX
    JA skip_tail
    VMOVDQU (SI)(CX*1), Y0
    VMOVDQU 32(SI)(CX*1), Y1
skip_classify:
    CLASSIFY
skip_masked:
    MOVQ R12, AX
    ORQ R10, AX
    JNZ skip_escapes
    TESTQ R9, R9
    JZ skip_plain
    TESTQ BX, BX
    JZ skip_instring        // all string contents
skip_plain:
    VMOVQ BX, X4
    VPCLMULQDQ $0, X10, X4, X4
    VMOVQ X4, AX
    XORQ R9, AX             // AX: bytes inside strings
skip_strings:
    MOVQ AX, R9
    SARQ $63, R9            // whether the next block starts in a string
    ANDNQ R13, AX, R13
    ANDNQ R14, AX, R14
skip_depth:
    TESTQ R14, R14
    JZ skip_next
    BLSIQ R14, AX           // lowest closing bracket
    LEAQ -1(AX), R11
    ANDQ R13, R11           // opening brackets before it
    POPCNTQ R11, R12
    LEAQ -1(R8)(R12*1), R8
    TESTQ R8, R8
    JZ skip_found
    ANDNQ R13, R11, R13
    XORQ AX, R14
    JMP skip_depth
skip_next:
    POPCNTQ R13, AX
    ADDQ AX, R8
    ADDQ $64, CX
    JMP skip_block

skip_found:
    TZCNTQ AX, AX
    LEAQ 1(CX)(AX*1), AX
    MOVQ AX, ret+24(FP)
    VZEROUPPER
    RET

skip_instring:
    // Inside a string with no escape pending: only a quote or a backslash
    // can change the state, so look for those alone.
    ADDQ $64, CX
skip_instring_loop:
    LEAQ 64(CX), AX
    CMPQ AX, DX
    JA skip_tail
    VMOVDQU (SI)(CX*1), Y0
    VMOVDQU 32(SI)(CX*1), Y1
    VPCMPEQB Y15, Y0, Y2
    VPCMPEQB Y15, Y1, Y3
    VPCMPEQB Y14, Y0, Y4
    VPCMPEQB Y14, Y1, Y5
    VPOR Y2, Y3, Y2
    VPOR Y4, Y5, Y4
    VPOR Y2, Y4, Y2
    VPTEST Y2, Y2
    JNZ skip_classify
    ADDQ $64, CX
    JMP skip_instring_loop

skip_escapes:
    // R12: backslashes, R10: whether byte 0 is escaped. A run of
    // backslashes escapes the byte after it when its length is odd, that
    // is when the run starts and ends on positions of different parity.
    // Adding the run starts to the backslashes carries each run to the
    // byte after it.
    VMOVQ R12, X6           // backslashes
    VMOVQ R13, X7           // opening brackets
    ANDNQ R12, R10, R12     // an escaped byte 0 starts no run
    MOVQ R12, AX
    SHLQ $1, AX
    ANDNQ R12, AX, R11      // run starts
    MOVQ $0x5555555555555555, AX
    ANDQ R11, AX            // starts on even positions
    ADDQ R12, AX
    ANDNQ AX, R12, AX       // ends of runs that started even
    MOVQ $0xaaaaaaaaaaaaaaaa, R13
    ANDQ R13, AX            // ... escape an odd position
    ANDQ R11, R13           // starts on odd positions
    ADDQ R12, R13
    SETCS R11B              // a run that started odd runs past the block
    ANDNQ R13, R12, R13     // ends of runs that started odd
    MOVQ $0x5555555555555555, R12
    ANDQ R12, R13           // ... escape an even position
    ORQ R13, AX
    ORQ R10, AX             // escaped bytes
    MOVBQZX R11B, R10
    ANDNQ BX, AX, BX        // unescaped quotes
    VMOVQ X7, R13
    VMOVQ BX, X4
    VPCLMULQDQ $0, X10, X4, X4
    VMOVQ X4, AX
    XORQ R9, AX             // AX: bytes inside strings
    // A backslash outside a string is invalid JSON, and the scalar scanner
    // does not treat it as an escape: let the caller use that one.
    VMOVQ X6, R11
    ANDNQ R11, AX, R11
    JNZ skip_slow
    JMP skip_strings

skip_tail:
    // Fewer than 64 bytes remain.
    MOVQ DX, AX
    SUBQ CX, AX
    JLE skip_end
    LEAQ (SI)(CX*1), R11
    ANDQ $4095, R11
    CMPQ R11, $(4096-64)
    JA skip_tail_page
    // The 64 bytes at pos lie in one memory page, which is mapped: read
    // them all and drop the bits past the end.
    VMOVDQU (SI)(CX*1), Y0
    VMOVDQU 32(SI)(CX*1), Y1
    CLASSIFY
    MOVQ DX, AX
    SUBQ CX, AX
    BZHIQ AX, BX, BX
    BZHIQ AX, R12, R12
    BZHIQ AX, R13, R13
    BZHIQ AX, R14, R14
    JMP skip_masked
skip_tail_page:
    // The block would cross into the next page. The 64 bytes that end at
    // the end of the input lie in the input or in the page of pos: read
    // those and shift the masks down so that bit 0 is byte pos.
    VMOVDQU -64(SI)(DX*1), Y0
    VMOVDQU -32(SI)(DX*1), Y1
    CLASSIFY
    LEAQ 64(CX), AX
    SUBQ DX, AX
    SHRXQ AX, BX, BX
    SHRXQ AX, R12, R12
    SHRXQ AX, R13, R13
    SHRXQ AX, R14, R14
    JMP skip_masked

skip_end:
    MOVQ R8, 0(DI)
    MOVQ R9, 8(DI)
    MOVQ R10, 16(DI)
    MOVQ CX, 24(DI)
    MOVQ $-1, ret+24(FP)
    VZEROUPPER
    RET

skip_slow:
    MOVQ $-4, ret+24(FP)
    VZEROUPPER
    RET

// Nibble tables of the path bytes: the class of a byte b is
// pathLo[b&15] & pathHi[b>>4]. Bit 4 marks '.'; bits 0 to 3 mark the bytes
// getComp handles: '"', '#' and '*' (bit 0), '?' (1), '@' (2), and '[',
// '\\', '{' and '|' (3). Every other byte, 0x80 and up included, has class 0.
DATA pathLo<>+0(SB)/8, $0x0000000001010004
DATA pathLo<>+8(SB)/8, $0x0210000808010000
GLOBL pathLo<>(SB), RODATA|NOPTR, $16
DATA pathHi<>+0(SB)/8, $0x0800080402110000
DATA pathHi<>+8(SB)/8, $0x0000000000000000
GLOBL pathHi<>(SB), RODATA|NOPTR, $16
// The nibble mask and the class of '.'.
DATA pathNib<>+0(SB)/8, $0x000000000000100f
GLOBL pathNib<>(SB), RODATA|NOPTR, $8

// PATHCLASSIFY leaves the masks of the dots in the 64 bytes in Y0 and Y1
// in R8, and of the other path bytes in R9.
#define PATHCLASSIFY \
    VBROADCASTI128 pathLo<>(SB), Y6; \
    VBROADCASTI128 pathHi<>(SB), Y7; \
    VPBROADCASTB pathNib<>+0(SB), Y8; \
    VPSRLW $4, Y0, Y2; \
    VPSRLW $4, Y1, Y3; \
    VPAND Y8, Y2, Y2; \
    VPAND Y8, Y3, Y3; \
    VPSHUFB Y2, Y7, Y2; \
    VPSHUFB Y3, Y7, Y3; \
    VPAND Y8, Y0, Y4; \
    VPAND Y8, Y1, Y5; \
    VPSHUFB Y4, Y6, Y4; \
    VPSHUFB Y5, Y6, Y5; \
    VPAND Y4, Y2, Y2; \
    VPAND Y5, Y3, Y3; \
    VPBROADCASTB pathNib<>+1(SB), Y8; \
    VPCMPEQB Y8, Y2, Y4; \
    VPCMPEQB Y8, Y3, Y5; \
    VPMOVMSKB Y4, R8; \
    VPMOVMSKB Y5, AX; \
    SHLQ $32, AX; \
    ORQ AX, R8; \
    VPXOR Y8, Y8, Y8; \
    VPCMPEQB Y8, Y2, Y4; \
    VPCMPEQB Y8, Y3, Y5; \
    VPMOVMSKB Y4, R9; \
    VPMOVMSKB Y5, AX; \
    SHLQ $32, AX; \
    ORQ AX, R9; \
    ORQ R8, R9; \
    NOTQ R9

// func pathMasksAVX2(p *byte, n int) (dots, special uint64)
//
// Returns the masks of the dots and of the other path bytes in the n bytes
// at p, 0 < n < 64. It reads 64 bytes that lie in the input or in a
// page the input touches, as skipAVX2 does for its last block.
TEXT ·pathMasksAVX2(SB), NOSPLIT, $0-32
    MOVQ p+0(FP), SI
    MOVQ n+8(FP), DX
    MOVQ SI, AX
    ANDQ $4095, AX
    CMPQ AX, $(4096-64)
    JA path_page
    VMOVDQU (SI), Y0
    VMOVDQU 32(SI), Y1
    PATHCLASSIFY
    BZHIQ DX, R8, R8
    BZHIQ DX, R9, R9
    JMP path_done
path_page:
    VMOVDQU -64(SI)(DX*1), Y0
    VMOVDQU -32(SI)(DX*1), Y1
    PATHCLASSIFY
    MOVQ $64, AX
    SUBQ DX, AX
    SHRXQ AX, R8, R8
    SHRXQ AX, R9, R9
path_done:
    MOVQ R8, dots+16(FP)
    MOVQ R9, special+24(FP)
    VZEROUPPER
    RET

// func membersAVX2(p *byte, n, i int, key *byte, klen int) (v, at int)
//
// memberValue on 32-byte loads: reads the members of an object from p[i],
// just after the opening brace or a comma, and returns the start of the
// value of the first whose key is the klen bytes at key, or -1, exactly as
// memberValue does. A member it leaves to memberValue makes it return
// membersSlow, with the start of that member in at: one whose key holds a
// backslash, or whose value skipAVX2 leaves to the scalar scanner.
//
// Strings end at the first quote that no backslash escapes, found 32 bytes
// at a time; whitespace and literals, which are short, go a byte at a time.
TEXT ·membersAVX2(SB), NOSPLIT, $72-56
    MOVQ p+0(FP), SI
    MOVQ n+8(FP), DX
    MOVQ i+16(FP), CX
    MOVQ key+24(FP), DI
    MOVQ klen+32(FP), R8
    MOVQ $0x100002600, R10      // whitespace: bits 9, 10, 13 and 32
    VMOVDQU jsonQuotes<>(SB), Y15
    VMOVDQU jsonSlashes<>(SB), Y14

mem_member:
    MOVQ CX, R9                 // start of the member
mem_key_space:
    CMPQ CX, DX
    JGE mem_notfound
    MOVBLZX (SI)(CX*1), AX
    CMPL AX, $0x22
    JEQ mem_key
    CMPL AX, $0x20
    JA mem_notfound
    BTQ AX, R10
    JCC mem_notfound
    INCQ CX
    JMP mem_key_space

mem_key:
    INCQ CX
    MOVQ CX, R11                // first byte of the key
    XORL R12, R12               // set at a backslash
mem_key_block:
    LEAQ 32(CX), AX
    CMPQ AX, DX
    JA mem_key_tail
    VMOVDQU (SI)(CX*1), Y0
    VPCMPEQB Y15, Y0, Y1
    VPCMPEQB Y14, Y0, Y2
    VPMOVMSKB Y1, AX            // quotes
    VPMOVMSKB Y2, BX            // backslashes
    LEAL -1(AX), R13
    XORL AX, R13                // bytes up to the first quote, or all
    TESTL R13, BX
    JNZ mem_key_escape
    TESTL AX, AX
    JZ mem_key_next
    TZCNTL AX, AX
    LEAQ 1(CX)(AX*1), CX        // past the closing quote
    JMP mem_key_end
mem_key_next:
    ADDQ $32, CX
    JMP mem_key_block
mem_key_escape:
    MOVL $1, R12
    TZCNTL BX, BX
    LEAQ 2(CX)(BX*1), CX        // past the backslash and the byte it escapes
    JMP mem_key_block
mem_key_tail:
    CMPQ CX, DX
    JGE mem_notfound
    MOVBLZX (SI)(CX*1), AX
    INCQ CX
    CMPL AX, $0x22
    JEQ mem_key_end
    CMPL AX, $0x5c
    JNE mem_key_tail
    MOVL $1, R12
    INCQ CX
    JMP mem_key_tail

mem_key_end:
    TESTL R12, R12
    JNZ mem_slow                // a key with escapes is decoded to compare
    XORL R13, R13               // set when the key matches
    LEAQ -1(CX), AX
    SUBQ R11, AX
    CMPQ AX, R8
    JNE mem_colon
    LEAQ (SI)(R11*1), R12
    XORL BX, BX
mem_cmp8:
    LEAQ 8(BX), AX
    CMPQ AX, R8
    JA mem_cmp1
    MOVQ (R12)(BX*1), AX
    CMPQ AX, (DI)(BX*1)
    JNE mem_colon
    ADDQ $8, BX
    JMP mem_cmp8
mem_cmp1:
    CMPQ BX, R8
    JGE mem_match
    MOVBLZX (R12)(BX*1), AX
    CMPB AL, (DI)(BX*1)
    JNE mem_colon
    INCQ BX
    JMP mem_cmp1
mem_match:
    MOVL $1, R13

mem_colon:
    CMPQ CX, DX
    JGE mem_notfound
    MOVBLZX (SI)(CX*1), AX
    CMPL AX, $0x3a
    JEQ mem_value_space
    CMPL AX, $0x20
    JA mem_notfound
    BTQ AX, R10
    JCC mem_notfound
    INCQ CX
    JMP mem_colon
mem_value_space:
    INCQ CX
    CMPQ CX, DX
    JGE mem_notfound
    MOVBLZX (SI)(CX*1), AX
    CMPL AX, $0x20
    JA mem_value
    BTQ AX, R10
    JCC mem_value               // a control byte starts a literal
    JMP mem_value_space

mem_value:
    TESTL R13, R13
    JNZ mem_found
    CMPL AX, $0x22
    JEQ mem_string
    CMPL AX, $0x7b
    JEQ mem_container
    CMPL AX, $0x5b
    JEQ mem_container
    LEAQ ·delim(SB), R11
mem_literal:
    MOVBLZX (R11)(AX*1), BX
    TESTL BX, BX
    JNZ mem_comma
    INCQ CX
    CMPQ CX, DX
    JGE mem_notfound
    MOVBLZX (SI)(CX*1), AX
    JMP mem_literal

mem_string:
    INCQ CX
mem_string_block:
    LEAQ 32(CX), AX
    CMPQ AX, DX
    JA mem_string_tail
    VMOVDQU (SI)(CX*1), Y0
    VPCMPEQB Y15, Y0, Y1
    VPCMPEQB Y14, Y0, Y2
    VPMOVMSKB Y1, AX
    VPMOVMSKB Y2, BX
    LEAL -1(AX), R13
    XORL AX, R13
    TESTL R13, BX
    JNZ mem_string_escape
    TESTL AX, AX
    JZ mem_string_next
    TZCNTL AX, AX
    LEAQ 1(CX)(AX*1), CX
    JMP mem_comma
mem_string_next:
    ADDQ $32, CX
mem_string_long:
    // A long string, such as a base64 payload: look 64 bytes at a time for
    // a quote or a backslash, then let the step above handle it.
    LEAQ 64(CX), AX
    CMPQ AX, DX
    JA mem_string_block
    VMOVDQU (SI)(CX*1), Y0
    VMOVDQU 32(SI)(CX*1), Y1
    VPCMPEQB Y15, Y0, Y2
    VPCMPEQB Y15, Y1, Y3
    VPCMPEQB Y14, Y0, Y4
    VPCMPEQB Y14, Y1, Y5
    VPOR Y2, Y3, Y2
    VPOR Y4, Y5, Y4
    VPOR Y2, Y4, Y2
    VPTEST Y2, Y2
    JNZ mem_string_block
    ADDQ $64, CX
    JMP mem_string_long
mem_string_escape:
    TZCNTL BX, BX
    LEAQ 2(CX)(BX*1), CX
    JMP mem_string_block
mem_string_tail:
    CMPQ CX, DX
    JGE mem_notfound
    MOVBLZX (SI)(CX*1), AX
    INCQ CX
    CMPL AX, $0x22
    JEQ mem_comma
    CMPL AX, $0x5c
    JNE mem_string_tail
    INCQ CX
    JMP mem_string_tail

mem_container:
    MOVQ R9, 64(SP)
    MOVQ SI, 0(SP)
    MOVQ DX, 8(SP)
    LEAQ 32(SP), AX
    MOVQ AX, 16(SP)
    MOVQ $0, 32(SP)             // depth
    MOVQ $0, 40(SP)             // inStr
    MOVQ $0, 48(SP)             // esc
    MOVQ CX, 56(SP)             // pos
    CALL ·skipAVX2(SB)
    MOVQ 24(SP), CX
    MOVQ p+0(FP), SI
    MOVQ n+8(FP), DX
    MOVQ key+24(FP), DI
    MOVQ klen+32(FP), R8
    MOVQ 64(SP), R9
    MOVQ $0x100002600, R10
    VMOVDQU jsonQuotes<>(SB), Y15
    VMOVDQU jsonSlashes<>(SB), Y14
    CMPQ CX, $-4
    JEQ mem_slow
    TESTQ CX, CX
    JLT mem_notfound

mem_comma:
    CMPQ CX, DX
    JGE mem_notfound
    MOVBLZX (SI)(CX*1), AX
    CMPL AX, $0x2c
    JEQ mem_next
    CMPL AX, $0x20
    JA mem_notfound
    BTQ AX, R10
    JCC mem_notfound
    INCQ CX
    JMP mem_comma
mem_next:
    INCQ CX
    JMP mem_member

mem_found:
    MOVQ CX, v+40(FP)
    MOVQ $0, at+48(FP)
    VZEROUPPER
    RET
mem_notfound:
    MOVQ $-1, v+40(FP)
    MOVQ $0, at+48(FP)
    VZEROUPPER
    RET
mem_slow:
    MOVQ $-3, v+40(FP)
    MOVQ R9, at+48(FP)
    VZEROUPPER
    RET
