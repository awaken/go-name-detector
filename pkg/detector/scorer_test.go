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
