// Package json encodes and decodes JSON with the API and the results of
// encoding/json, written for speed and low memory use.
//
// It has the API of encoding/json in Go 1.26 and the results of the
// encoding/json it is built with: the same bytes from Marshal, the same
// values from Unmarshal and the same errors. That is the classic
// encoding/json, or the one built on encoding/json/v2, as it is by default
// from Go 1.27 and with GOEXPERIMENT=jsonv2 from Go 1.25.
//
// RawMessage, Number, Marshaler, Unmarshaler, Delim, Token and the
// error types are aliases of encoding/json's, so values, type switches and
// errors.As work across both packages.
//
// The one addition is UnmarshalNoCopy, which stores strings that view the
// input instead of copies, for callers that keep the input unchanged while
// they use the decoded values.
//
// Differences that do not change results:
//
//   - Marshal of a value nested more than 1<<20 levels deep (not a cycle)
//     returns an *UnsupportedValueError. encoding/json recurses until the
//     goroutine stack overflows, which ends the process.
//   - Short strings, RawMessage copies, small scalars behind pointers and
//     the strings and numbers held by interface values that Unmarshal
//     stores share small blocks of at most 4 KiB per call. Keeping one
//     such value keeps its block alive; encoding/json keeps only the value
//     itself.
//   - A Decoder whose reader can seek, such as an *os.File, reads a value
//     longer than 128 KiB twice: once to check it and once to decode it,
//     with long strings read directly into their own allocations. It holds
//     the decoded value and a window of 256 KiB, not the input. Code that
//     watches the calls on the reader sees the extra reads and seeks, and
//     what Buffered returns after such a value is empty. If the input
//     changes between the two reads, Decode returns an error, the value
//     may be partly set, and the Decoder stops.
package json
