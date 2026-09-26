package detector

import (
	"math"
	"testing"

	"github.com/montevive/go-name-detector/pkg/types"
)

func TestPatternRankRoles(t *testing.T) {
	for _, tc := range []struct {
		name        string
		firstRank   int32
		lastRank    int32
		shadowFirst int32
		shadowLast  int32
		want        float64
	}{
		{"top ten", 1, 1, 0, 0, 0.96},
		{"rare given-name homonym", 1, 1, 580, 0, 0.96},
		{"top hundred", 50, 50, 0, 0, 0.742},
		{"top ten boundary", 10, 10, 0, 0, 0.96},
		{"top hundred boundary", 100, 100, 0, 0, 0.595},
		{"outside top hundred", 101, 101, 0, 0, 0.425},
		{"rare surname common given name", 1, 1000, 1, 0, 0.46},
		{"rare given name common surname", 1000, 1, 0, 1, 0.46},
		{"missing given rank", 0, 1, 0, 1, 0.425},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := &types.NameDataset{
				FirstNames: map[string]*types.NameData{"ALPHA": {Rank: map[string]int32{"US": tc.firstRank}}},
				LastNames:  map[string]*types.NameData{"BETA": {Rank: map[string]int32{"GB": tc.lastRank}}},
			}
			if tc.shadowFirst != 0 {
				data.FirstNames["BETA"] = &types.NameData{Rank: map[string]int32{"US": tc.shadowFirst}}
			}
			if tc.shadowLast != 0 {
				data.LastNames["ALPHA"] = &types.NameData{Rank: map[string]int32{"GB": tc.shadowLast}}
			}
			got := New(data).DetectPII([]string{"Alpha", "Beta"})
			if math.Abs(got.Confidence-tc.want) > 1e-9 {
				t.Fatalf("score = %.9f, want %.9f", got.Confidence, tc.want)
			}
		})
	}
}

func TestPatternRankNormalization(t *testing.T) {
	data := &types.NameDataset{
		FirstNames: map[string]*types.NameData{
			"JOSE":   {Rank: map[string]int32{"ES": 1}},
			"GARCIA": {Rank: map[string]int32{"ES": 1000}},
		},
		LastNames: map[string]*types.NameData{"GARCIA": {Rank: map[string]int32{"ES": 1}}},
	}
	got := New(data).DetectPII([]string{"José", "García"})
	if math.Abs(got.Confidence-0.96) > 1e-9 {
		t.Fatalf("accented roles: score = %.9f, want 0.96", got.Confidence)
	}
}

func TestAuditBonusRequiresMatches(t *testing.T) {
	s := NewScorer(createTestDataset(), ScoreConfig{BaseMatchScore: 0.3, MultipleNamesBonus: 0.15})
	for _, tc := range []struct {
		combo types.NameCombination
		want  float64
	}{
		{types.NameCombination{FirstNames: []string{"unknown"}, Surnames: []string{"absent", "missing"}}, 0},
		{types.NameCombination{FirstNames: []string{"John", "unknown"}, Surnames: []string{"Smith"}}, 0.2},
		{types.NameCombination{FirstNames: []string{"John", "Jose"}, Surnames: []string{"Smith"}}, 0.45},
	} {
		if score := s.ScoreCombination(tc.combo); math.Abs(score-tc.want) > 1e-9 {
			t.Errorf("score of %+v = %v, want %v; bonus needs three dictionary matches", tc.combo, score, tc.want)
		}
	}
}

func TestAuditScoreBounds(t *testing.T) {
	data := createTestDataset()
	combo := types.NameCombination{FirstNames: []string{"John"}, Surnames: []string{"Smith"}}
	s := NewScorer(data, ScoreConfig{BaseMatchScore: -1})
	if score := s.ScoreCombination(combo); score != 0 {
		t.Errorf("confidence must remain in [0,1], got %v", score)
	}
	if score := NewScorer(data, ScoreConfig{BaseMatchScore: 2}).ScoreCombination(combo); score != 1 {
		t.Errorf("upper confidence bound = %v", score)
	}
	for _, combo := range []types.NameCombination{{}, {FirstNames: []string{"John"}}, {Surnames: []string{"Smith"}}} {
		if score := s.ScoreCombination(combo); score != 0 {
			t.Errorf("incomplete combination scored %v", score)
		}
	}
	for _, ranks := range []map[string]int32{nil, {"US": -1}, {"US": 0}} {
		if score := s.calculatePopularityScore(&types.NameData{Rank: ranks}); score != 0 {
			t.Errorf("unknown rank scored %v", score)
		}
	}
	if score := s.calculatePopularityScore(&types.NameData{Rank: map[string]int32{"US": math.MaxInt32}}); score != 0.02 {
		t.Errorf("largest positive rank must receive the rare-name score, got %v", score)
	}
	if rank := s.getMinRankFromData(&types.NameData{}); rank != 999999 {
		t.Errorf("missing rank = %d", rank)
	}
}

func TestAuditScoreMetadata(t *testing.T) {
	s := NewScorer(createTestDataset(), DefaultScoreConfig())
	for _, tc := range []struct {
		first, last []string
		country     string
		gender      string
	}{
		{nil, nil, "", ""},
		{[]string{"missing"}, []string{"absent"}, "", ""},
		{[]string{"José"}, []string{"García"}, "MX", "Male"},
		{[]string{"Maria"}, []string{"Lopez"}, "MX", "Female"},
		{[]string{"John"}, []string{"Smith"}, "US", "Male"},
	} {
		combo := types.NameCombination{FirstNames: tc.first, Surnames: tc.last}
		if country, gender := s.GetTopCountry(combo), s.GetGender(combo); country != tc.country || gender != tc.gender {
			t.Errorf("metadata of %+v = %q, %q", combo, country, gender)
		}
	}
	for _, tc := range []struct {
		data []*types.NameData
		want float64
	}{
		{nil, 0}, {[]*types.NameData{{}, {}}, 0},
		{[]*types.NameData{{Gender: map[string]float32{"M": 1}}, {Gender: map[string]float32{"F": 1}}}, 0},
		{[]*types.NameData{{Gender: map[string]float32{"M": 1}}, {Gender: map[string]float32{"M": 1}}}, 0.1},
	} {
		if got := s.calculateGenderConsistency(tc.data); math.Abs(got-tc.want) > 1e-6 {
			t.Errorf("gender bonus = %v, want %v", got, tc.want)
		}
	}
	first := []*types.NameData{{Country: map[string]float32{"US": 0.3, "GB": 0.7}}}
	last := []*types.NameData{{Country: map[string]float32{"US": 1}}}
	if got := s.calculateCountryOverlap(first, last); math.Abs(got-0.045) > 1e-6 {
		t.Errorf("country overlap = %v", got)
	}
	if got := s.calculateCountryOverlap(nil, nil); got != 0 {
		t.Errorf("missing country overlap = %v", got)
	}
	combo := types.NameCombination{FirstNames: []string{"de"}, Surnames: []string{"van"}}
	if got := s.applyPatternAdjustments(combo, 1, nil, nil); math.Abs(got-0.21) > 1e-9 {
		t.Errorf("connector penalty = %v", got)
	}
}
