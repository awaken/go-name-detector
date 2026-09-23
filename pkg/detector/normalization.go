// Modified by Flower: use stack buffers for word checks and dictionary lookup.
package detector

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/montevive/go-name-detector/pkg/types"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// lowerNameWord folds short words for the ASCII stop-word and connector tables.
// Unicode case mappings such as İ -> i still apply; other non-ASCII words cannot match.
func lowerNameWord(word string) (buf [6]byte, n int) {
	for _, r := range word {
		r = unicode.ToLower(r)
		if r >= utf8.RuneSelf || n == len(buf) {
			return buf, 0
		}
		buf[n] = byte(r)
		n++
	}
	return buf, n
}

// lookupName avoids allocating uppercase keys for ordinary names. The map lookup
// borrows the stack bytes; long names and accent fallback retain the general path.
func lookupName(name string, dataset map[string]*types.NameData) (*types.NameData, bool) {
	name = strings.TrimSpace(name)
	var buf [128]byte
	n := 0
	ascii := true
	for _, r := range name {
		ascii = ascii && r < utf8.RuneSelf
		r = unicode.ToUpper(r)
		if n+utf8.RuneLen(r) > len(buf) {
			key := strings.ToUpper(name)
			if data, ok := dataset[key]; ok {
				return data, true
			}
			data, ok := dataset[normalizeForLookup(name)]
			return data, ok
		}
		n += utf8.EncodeRune(buf[n:], r)
	}
	if data, ok := dataset[string(buf[:n])]; ok {
		return data, true
	}
	if ascii {
		return nil, false
	}
	data, ok := dataset[normalizeForLookup(name)]
	return data, ok
}

// normalizeAccents removes accents and diacritical marks from a string
// Example: "José García" -> "Jose Garcia"
func normalizeAccents(s string) string {
	ascii := true
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	// Create a transformer that removes accents by decomposing Unicode characters
	// and then removing the combining diacritical marks
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

	// Apply the transformation
	result, _, err := transform.String(t, s)
	if err != nil {
		// If transformation fails, return original string
		return s
	}

	return result
}

// normalizeForLookup normalizes a name for database lookup
// This applies both accent normalization and case normalization
func normalizeForLookup(name string) string {
	// First normalize accents, then trim and convert to uppercase
	normalized := normalizeAccents(name)
	return strings.ToUpper(strings.TrimSpace(normalized))
}
