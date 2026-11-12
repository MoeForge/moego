package ojson

import "github.com/MoeForge/moego/internal/jsonsimd"

// Valid reports whether json is valid JSON: one value (RFC 8259) surrounded
// only by whitespace, at any depth.
func Valid(json string) bool { return jsonsimd.Validate(json, 0, nil) }

// ValidBytes is Valid for a byte slice.
func ValidBytes(json []byte) bool { return Valid(bytesView(json)) }
