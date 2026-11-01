// Package ojson reads and edits JSON without reordering it. Its package-level
// functions match github.com/tidwall/gjson v1.19.0 and
// github.com/tidwall/sjson v1.2.5, so code can switch by changing the import:
//
//	import gjson "github.com/MoeForge/moego/ojson"
//	import sjson "github.com/MoeForge/moego/ojson"
//
// On valid JSON the results are the same as those of gjson and sjson,
// except in these cases, where those libraries fail:
//
//   - An array index more than 16,777,216 past the end returns an error,
//     where sjson pads the array with nulls until memory runs out.
//   - Valid keeps its own stack, so deep nesting cannot overflow the
//     goroutine stack.
//   - With the Optimistic and ReplaceInPlace options, a string value that
//     needs escaping is set, where sjson leaves the document unchanged.
//   - Set, SetRaw and Delete never turn a valid document into invalid JSON.
//     On a query path, sjson writes the value at the offsets of whatever
//     Get finds there, including the output of modifiers, counts and
//     offsets that do not point at a value of the document. ojson sets only
//     values that stand at those offsets, and otherwise leaves the
//     document unchanged without an error. Where a plain path runs into a
//     string, sjson edits the text inside the string; ojson replaces the
//     string.
//   - Set and SetRaw never add a member under a key its object already
//     has. sjson finds members by reading each path component as a Get
//     path, so it misses a member whose key starts with '[', '{' or '!'
//     and adds a second one. ojson replaces the member the key names.
//   - The @pretty, @dig and @group modifiers return an empty value when
//     their output would exceed 64 MiB plus 16 times the size of their
//     input, which bounds the memory a path can make them use.
//
// On malformed JSON, which Valid reports as invalid, the results may differ
// from those of gjson and sjson. The only promise there is that no call
// panics, overflows the stack or uses unbounded memory.
//
// Edits keep every untouched byte and the order of members. For several
// edits to one document, use a Doc: it applies them in memory and writes the
// result once.
//
//	doc := ojson.NewDoc(body)
//	doc.Set("temperature", 0.2)
//	doc.Set("messages.0.content", "hello")
//	body = doc.Bytes()
package ojson
