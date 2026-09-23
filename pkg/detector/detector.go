// Modified by Flower: provide scoring without details and avoid per-word maps.
package detector

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/montevive/go-name-detector/pkg/loader"
	"github.com/montevive/go-name-detector/pkg/types"
)

// Detector handles PII name detection
type Detector struct {
	scorer *Scorer
}

// New creates a new Detector with the given dataset
func New(dataset *types.NameDataset) *Detector {
	config := DefaultScoreConfig()
	scorer := NewScorer(dataset, config)

	return &Detector{
		scorer: scorer,
	}
}

// NewWithConfig creates a new Detector with custom scoring configuration
func NewWithConfig(dataset *types.NameDataset, config ScoreConfig) *Detector {
	scorer := NewScorer(dataset, config)

	return &Detector{
		scorer: scorer,
	}
}

// NewDefault creates a new Detector with embedded dataset - ready to use out of the box
func NewDefault() (*Detector, error) {
	l, err := loader.NewWithEmbeddedData()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize detector with embedded data: %w", err)
	}

	return New(l.GetDataset()), nil
}

// DetectPII analyzes words to determine if they represent a PII name
func (d *Detector) DetectPII(words []string) types.PIIResult {
	return d.DetectPIIWithThreshold(words, 0.7) // Default threshold
}

// DetectPIIWithThreshold analyzes words with a custom confidence threshold
func (d *Detector) DetectPIIWithThreshold(words []string, threshold float64) types.PIIResult {
	if len(words) < 2 || len(words) > 6 {
		return types.PIIResult{
			IsLikelyName: false,
			Confidence:   0.0,
			Details: types.NameDetails{
				Pattern: "invalid_length",
			},
		}
	}

	// Clean and normalize words
	cleanWords := d.cleanWords(words)
	if len(cleanWords) < 2 {
		return types.PIIResult{
			IsLikelyName: false,
			Confidence:   0.0,
			Details: types.NameDetails{
				Pattern: "insufficient_words",
			},
		}
	}

	// Generate all possible name combinations
	combinations := d.generateCombinations(cleanWords)

	// Score each combination and find the best one
	bestCombo, bestScore := d.findBestCombination(combinations)

	// Determine if it's likely a name
	isLikelyName := bestScore >= threshold

	// Build result details
	pattern := d.buildPattern(bestCombo)
	topCountry := d.scorer.GetTopCountry(bestCombo)
	gender := d.scorer.GetGender(bestCombo)

	return types.PIIResult{
		IsLikelyName: isLikelyName,
		Confidence:   bestScore,
		Details: types.NameDetails{
			FirstNames: bestCombo.FirstNames,
			Surnames:   bestCombo.Surnames,
			Pattern:    pattern,
			TopCountry: topCountry,
			Gender:     gender,
		},
	}
}

// Score returns confidence and the number of words in the winning combination.
// It uses the same scoring rules as DetectPII without building descriptive data.
// Inputs outside two to six words, or without a positive score, return zeroes.
func (d *Detector) Score(words []string) (confidence float64, used int) {
	if len(words) < 2 || len(words) > 6 {
		return 0, 0
	}
	var buf [6]string
	cleaned := d.cleanWordsInto(buf[:0], words)
	for i := 1; i < len(cleaned); i++ {
		combo := types.NameCombination{FirstNames: cleaned[:i], Surnames: cleaned[i:]}
		if score := d.scorer.ScoreCombination(combo); score > confidence {
			confidence, used = score, len(cleaned)
		}
	}
	return confidence, used
}

// cleanWords removes empty strings, trims whitespace, and filters invalid words.
func (d *Detector) cleanWords(words []string) []string {
	return d.cleanWordsInto(make([]string, 0, len(words)), words)
}

func (d *Detector) cleanWordsInto(cleaned, words []string) []string {
	for _, word := range words {
		word = strings.TrimSpace(word)
		if len(word) == 0 {
			continue
		}

		// Skip words that are clearly not names (too short, numbers, special chars)
		if d.isValidNameWord(word) {
			cleaned = append(cleaned, word)
		}
	}

	return cleaned
}

// isValidNameWord checks if a word could plausibly be part of a name
func (d *Detector) isValidNameWord(word string) bool {
	// Must be at least 2 characters
	if len(word) < 2 {
		return false
	}

	// Must contain only letters (and possibly hyphens, apostrophes, dots)
	for _, r := range word {
		if !(unicode.IsLetter(r) || r == '-' || r == '\'' || r == '.') {
			return false
		}
	}

	// Skip common non-name words
	buf, n := lowerNameWord(word)
	switch string(buf[:n]) {
	case "the", "and", "or", "but", "in", "on", "at", "to", "for", "of", "with", "by",
		"is", "are", "was", "were", "be", "been", "have", "has", "had", "do", "does", "did",
		"will", "would", "could", "should", "may", "might", "can", "must", "shall", "this",
		"that", "these", "those", "a", "an", "it", "he", "she", "they", "we", "you", "i",
		"me", "him", "her", "them", "us", "my", "your", "his", "our", "their", "its":
		return false
	}
	return true
}

// isProbablyPreposition checks if a word is likely a preposition/connector
// that shouldn't be counted as a first or last name
func (d *Detector) isProbablyPreposition(word string) bool {
	return isNamePreposition(word)
}

// generateCombinations creates all possible splits of words into first names and surnames
func (d *Detector) generateCombinations(words []string) []types.NameCombination {
	var combinations []types.NameCombination

	// Try all possible splits where at least 1 word is first name and 1 is surname
	for i := 1; i < len(words); i++ {
		combo := types.NameCombination{
			FirstNames: words[:i],
			Surnames:   words[i:],
		}
		combinations = append(combinations, combo)
	}

	return combinations
}

// findBestCombination scores all combinations and returns the best one
func (d *Detector) findBestCombination(combinations []types.NameCombination) (types.NameCombination, float64) {
	var bestCombo types.NameCombination
	var bestScore float64

	for _, combo := range combinations {
		score := d.scorer.ScoreCombination(combo)
		if score > bestScore {
			bestScore = score
			bestCombo = combo
		}
	}

	return bestCombo, bestScore
}

// buildPattern creates a pattern string describing the name structure
func (d *Detector) buildPattern(combo types.NameCombination) string {
	firstCount := len(combo.FirstNames)
	lastCount := len(combo.Surnames)

	return fmt.Sprintf("%d_first_%d_last", firstCount, lastCount)
}

// GetDatasetStats returns statistics about the loaded dataset
func (d *Detector) GetDatasetStats() map[string]interface{} {
	if d.scorer == nil || d.scorer.dataset == nil {
		return map[string]interface{}{
			"error": "dataset not loaded",
		}
	}

	return map[string]interface{}{
		"first_names_count": len(d.scorer.dataset.FirstNames),
		"last_names_count":  len(d.scorer.dataset.LastNames),
	}
}
