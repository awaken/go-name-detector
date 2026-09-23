package detector

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/montevive/go-name-detector/pkg/types"
)

func TestNormalizeAccents(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"José", "Jose"},
		{"García", "Garcia"},
		{"François", "Francois"},
		{"Müller", "Muller"},
		{"Andrés", "Andres"},
		{"María", "Maria"},
		{"González", "Gonzalez"},
		{"Hernández", "Hernandez"},
		{"López", "Lopez"},
		{"Martínez", "Martinez"},
		{"Rodríguez", "Rodriguez"},
		{"Sánchez", "Sanchez"},
		{"Pérez", "Perez"},
		{"Ramón", "Ramon"},
		{"Ángel", "Angel"},
		{"José Manuel", "Jose Manuel"},
		{"María García", "Maria Garcia"},
		{"John Smith", "John Smith"}, // No accents should remain unchanged
		{"", ""},                     // Empty string
	}

	for _, tt := range tests {
		result := normalizeAccents(tt.input)
		if result != tt.expected {
			t.Errorf("normalizeAccents(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestNormalizeForLookup(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"José", "JOSE"},
		{"García", "GARCIA"},
		{"  José  ", "JOSE"}, // Test trimming
		{"josé", "JOSE"},     // Test case conversion
		{"JOSÉ", "JOSE"},
		{"María García", "MARIA GARCIA"},
		{"John Smith", "JOHN SMITH"},
	}

	for _, tt := range tests {
		result := normalizeForLookup(tt.input)
		if result != tt.expected {
			t.Errorf("normalizeForLookup(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// Benchmark the normalization function
func BenchmarkNormalizeAccents(b *testing.B) {
	testNames := []string{"José", "García", "François", "Müller", "María García López"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, name := range testNames {
			normalizeAccents(name)
		}
	}
}

func TestLowerNameWord(t *testing.T) {
	for _, word := range []string{"", "THE", "Should", "SHOULDNOT", "İn", "İT", "doſ", "K", "José", "\xff", "van", "AND", "İİİİİİ", "İİİİİİİ"} {
		want := strings.ToLower(word)
		if len(want) > 6 {
			want = ""
		}
		for _, b := range []byte(want) {
			if b >= utf8.RuneSelf {
				want = ""
				break
			}
		}
		buf, n := lowerNameWord(word)
		if got := string(buf[:n]); got != want {
			t.Errorf("fold %q = %q, want %q", word, got, want)
		}
	}
}

func TestNameLookup(t *testing.T) {
	for _, word := range []string{"", "John", "JOHN", "José", "Jose\u0301", "Straße", "İn", "ıan", "ſmith", "Karl", "ǆuro", "𐐨name", "張", "\xff", strings.Repeat("a", 128), strings.Repeat("a", 129), strings.Repeat("é", 100)} {
		for _, padded := range []bool{false, true} {
			name := word
			if padded {
				name = " \t" + word + "\u00a0"
			}
			key := strings.ToUpper(strings.TrimSpace(name))
			normalized := normalizeForLookup(name)
			exact, fallback := &types.NameData{}, &types.NameData{}
			data := map[string]*types.NameData{normalized: fallback, key: exact}
			if got, ok := lookupName(name, data); !ok || got != exact {
				t.Fatalf("exact %q = %p, %v", name, got, ok)
			}
			delete(data, key)
			want := data[normalized]
			if got, ok := lookupName(name, data); got != want || ok != (want != nil) {
				t.Fatalf("fallback %q = %p, %v; want %p", name, got, ok, want)
			}
		}
	}
}

func TestNormalizeAccentEdges(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""}, {"John Smith", "John Smith"}, {"José García", "Jose Garcia"},
		{"Jose\u0301", "Jose"}, {"李 王", "李 王"}, {"O'Connor-Smith", "O'Connor-Smith"},
	} {
		if got := normalizeAccents(tc.input); got != tc.want {
			t.Errorf("normalize %q = %q, want %q", tc.input, got, tc.want)
		}
	}
}
