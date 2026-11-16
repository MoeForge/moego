// Package deflate is moego's DEFLATE encoder with zlib framing (RFC 1950
// and RFC 1951), written for moego/png. Level 1 matches greedily against
// the two latest positions of each hash, refreshed at the end of every
// match, levels 2 to 7 use hash chains with lazy matching, and levels 8
// and 9 search a binary tree and parse by bit cost.
// Every block is written as the cheapest of the stored, fixed-code and
// dynamic-code forms.
//
// The overall designs of level 1 (a hash table that keeps the two latest
// positions per bucket) and of levels 8 and 9 (a binary-tree match finder
// that lists the nearest match of every length, then a parse that
// minimizes the coded size with costs taken from the previous pass's
// Huffman codes) follow those of libdeflate; the code is written from the
// DEFLATE specification and does not derive from libdeflate's.
package deflate
