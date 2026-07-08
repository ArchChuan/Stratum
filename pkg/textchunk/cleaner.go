// Package textchunk provides text chunking and cleaning utilities.
package textchunk

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	// reControlChars strips non-printable control characters (except \t, \n, \r).
	reControlChars = regexp.MustCompile(`[\x00-\x08\x0b\x0e-\x1f\x7f]`)

	// reExcessiveLines collapses 3+ consecutive newlines into exactly 2.
	reExcessiveLines = regexp.MustCompile(`\n{3,}`)

	// reHeaderFooter matches common page header/footer patterns in PDFs.
	// Examples: "第3页 共10页", "Page 3 of 10", "3 / 10 页"
	reHeaderFooter = regexp.MustCompile(`(?im)^(第\s*\d+\s*页[^\n]*|[Pp]age\s+\d+(\s+of\s+\d+)?[^\n]*|\d+\s*/\s*\d+\s*页[^\n]*)$\n?`)
)

const (
	// simhashMaxDistance is the Hamming distance threshold for near-duplicate detection.
	// Chunks with simhash distance ≤ 3 bits (out of 64) are considered duplicates.
	simhashMaxDistance = 3

	// minContentRunes is the minimum number of letter/digit runes a chunk must contain
	// to avoid being filtered as non-content.
	minContentRunes = 10
)

// TextCleaner normalizes raw extracted text and filters low-quality chunks.
type TextCleaner struct{}

// NewTextCleaner creates a TextCleaner instance.
func NewTextCleaner() *TextCleaner {
	return &TextCleaner{}
}

// Clean normalizes raw document text before chunking.
// Applies: control character stripping, header/footer removal,
// excessive newline merging, and trimming.
func (tc *TextCleaner) Clean(text string) string {
	// 1. Strip control characters (keep \t, \n, \r)
	text = reControlChars.ReplaceAllString(text, "")

	// 2. Remove header/footer patterns (e.g., "Page 3 of 10")
	text = reHeaderFooter.ReplaceAllString(text, "")

	// 3. Merge excessive consecutive newlines (3+ → 2)
	text = reExcessiveLines.ReplaceAllString(text, "\n\n")

	// 4. Trim leading/trailing whitespace
	return strings.TrimSpace(text)
}

// FilterChunks removes low-quality and near-duplicate chunks.
// Filters applied:
// - Empty chunks or pure punctuation/whitespace
// - Chunks with fewer than minContentRunes letters/digits
// - Near-duplicate chunks (simhash Hamming distance ≤ simhashMaxDistance)
func (tc *TextCleaner) FilterChunks(chunks []TextChunk) []TextChunk {
	seen := make([]uint64, 0, len(chunks))
	out := chunks[:0] // reuse underlying array

	for _, c := range chunks {
		// Skip pure non-content (only punctuation/whitespace)
		if isPureNonContent(c.Content) {
			continue
		}

		// Skip chunks with too few content runes
		if countContentRunes(c.Content) < minContentRunes {
			continue
		}

		// Skip near-duplicate chunks (simhash-based)
		h := computeSimhash(c.Content)
		if isDuplicate(h, seen) {
			continue
		}

		seen = append(seen, h)
		out = append(out, c)
	}

	return out
}

// isPureNonContent returns true if the string contains no letters or digits.
func isPureNonContent(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// countContentRunes counts letters and digits in the string.
func countContentRunes(s string) int {
	count := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			count++
		}
	}
	return count
}

// isDuplicate checks if the given simhash is a near-duplicate of any seen hash.
// Uses Hamming distance threshold simhashMaxDistance.
func isDuplicate(h uint64, seen []uint64) bool {
	for _, s := range seen {
		if hammingDistance64(h^s) <= simhashMaxDistance {
			return true
		}
	}
	return false
}

// hammingDistance64 computes the Hamming distance (popcount) of a 64-bit integer.
// Uses the SWAR (SIMD Within A Register) algorithm.
func hammingDistance64(x uint64) int {
	x -= (x >> 1) & 0x5555555555555555
	x = (x & 0x3333333333333333) + ((x >> 2) & 0x3333333333333333)
	x = (x + (x >> 4)) & 0x0f0f0f0f0f0f0f0f
	return int((x * 0x0101010101010101) >> 56)
}

// computeSimhash computes a 64-bit locality-sensitive hash using character trigrams.
// Chunks with similar content produce similar simhash values (low Hamming distance).
func computeSimhash(text string) uint64 {
	runes := []rune(strings.ToLower(strings.TrimSpace(text)))
	n := len(runes)
	if n == 0 {
		return 0
	}

	// Use character trigrams (3-grams) for fingerprinting.
	step := min(3, n)

	var counts [64]int32
	for i := 0; i <= n-step; i++ {
		h := fnv64a(string(runes[i : i+step]))
		for bit := range 64 {
			if h>>uint(bit)&1 == 1 {
				counts[bit]++
			} else {
				counts[bit]--
			}
		}
	}

	var result uint64
	for bit := range 64 {
		if counts[bit] > 0 {
			result |= 1 << uint(bit)
		}
	}
	return result
}

// fnv64a computes the FNV-1a 64-bit hash of a string.
func fnv64a(s string) uint64 {
	const (
		offset uint64 = 14695981039346656037
		prime  uint64 = 1099511628211
	)
	h := offset
	for _, b := range []byte(s) {
		h ^= uint64(b)
		h *= prime
	}
	return h
}
