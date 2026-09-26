package loader

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	names "github.com/montevive/go-name-detector/pkg/proto"
	"google.golang.org/protobuf/proto"
)

func auditTemp(t *testing.T) string {
	t.Helper()
	base := os.Getenv("FLOWER_AUDIT_TMP")
	if base == "" {
		base = "../../../../../tmp"
	}
	dir, err := os.MkdirTemp(base, "name-loader-audit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func auditMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func auditGzip(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func auditWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func auditDataset() *names.CombinedNameDataset {
	return &names.CombinedNameDataset{
		FirstNames: &names.NameDataset{Entries: []*names.NameEntry{{Name: " First ", Country: map[string]float32{"IT": 1}, Gender: map[string]float32{"F": 1}, Rank: map[string]int32{"IT": 2}}}},
		LastNames:  &names.NameDataset{Entries: []*names.NameEntry{{Name: " Last ", Country: map[string]float32{"IT": 1}, Rank: map[string]int32{"IT": 3}}}},
	}
}

func auditLoaded(t *testing.T, l *Loader) {
	t.Helper()
	data := l.GetDataset()
	if !l.IsLoaded() || len(data.FirstNames) != 1 || len(data.LastNames) != 1 {
		t.Fatalf("invalid load state: %+v", l.GetStats())
	}
	if data.FirstNames["FIRST"].Rank["IT"] != 2 || data.LastNames["LAST"].Country["IT"] != 1 {
		t.Errorf("metadata or normalized keys changed: %+v", data)
	}
}

func TestAuditLoaderFormats(t *testing.T) {
	dir := auditTemp(t)
	data := auditMarshal(t, auditDataset())
	for _, zipped := range []bool{false, true} {
		for _, file := range []bool{false, true} {
			l := New()
			if l.IsLoaded() || l.GetStats()["first_names_count"] != 0 {
				t.Fatal("new loader is not empty")
			}
			content := data
			name := filepath.Join(dir, "combined.pb")
			if zipped {
				content = auditGzip(t, data)
				name += ".gz"
			}
			var err error
			if file {
				auditWrite(t, name, content)
				err = l.LoadFromFile(name)
			} else {
				err = l.LoadFromBytes(content)
			}
			if err != nil {
				t.Fatal(err)
			}
			auditLoaded(t, l)
			// A loaded instance preserves its first dataset across repeated loads.
			if err := l.LoadFromBytes([]byte{0xff}); err != nil {
				t.Fatal(err)
			}
			if err := l.LoadFromFile("missing"); err != nil {
				t.Fatal(err)
			}
			if err := l.LoadSeparateFiles("missing", "missing"); err != nil {
				t.Fatal(err)
			}
			auditLoaded(t, l)
		}
	}
	first, last := filepath.Join(dir, "first.pb"), filepath.Join(dir, "last.pb.gz")
	auditWrite(t, first, auditMarshal(t, auditDataset().FirstNames))
	auditWrite(t, last, auditGzip(t, auditMarshal(t, auditDataset().LastNames)))
	l := New()
	if err := l.LoadSeparateFiles(first, last); err != nil {
		t.Fatal(err)
	}
	auditLoaded(t, l)
}

func TestAuditLoaderErrors(t *testing.T) {
	dir := auditTemp(t)
	valid := auditGzip(t, auditMarshal(t, auditDataset()))
	badCRC := append([]byte(nil), valid...)
	badCRC[len(badCRC)-8] ^= 1
	for _, data := range [][]byte{{0xff}, {0x1f, 0x8b}, badCRC} {
		l := New()
		if err := l.LoadFromBytes(data); err == nil || l.IsLoaded() {
			t.Errorf("invalid bytes accepted: %v", err)
		}
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"bad.pb", []byte{0xff}}, {"header.gz", []byte{0x1f, 0x8b}}, {"crc.gz", badCRC},
	} {
		path := filepath.Join(dir, tc.name)
		auditWrite(t, path, tc.data)
		if err := New().LoadFromFile(path); err == nil {
			t.Errorf("invalid file %s accepted", tc.name)
		}
		if err := New().LoadSeparateFiles(path, path); err == nil {
			t.Errorf("invalid separate file %s accepted", tc.name)
		}
	}
	for _, path := range []string{"missing", "missing.gz"} {
		if err := New().LoadFromFile(filepath.Join(dir, path)); err == nil {
			t.Error("missing file accepted")
		}
		if err := New().LoadSeparateFiles(filepath.Join(dir, path), "missing"); err == nil {
			t.Error("missing first dataset accepted")
		}
	}
}

func TestAuditMissingSections(t *testing.T) {
	for _, dataset := range []*names.CombinedNameDataset{
		{}, {FirstNames: &names.NameDataset{}}, {LastNames: &names.NameDataset{}},
	} {
		t.Run(dataset.String(), func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("valid protobuf with absent sections panicked: %v", p)
				}
			}()
			l := New()
			err := l.LoadFromBytes(auditMarshal(t, dataset))
			if l.IsLoaded() != (err == nil) {
				t.Error("load state disagrees with returned error")
			}
		})
	}
}

func TestAuditSeparateLoadAtomic(t *testing.T) {
	dir := auditTemp(t)
	first, last := filepath.Join(dir, "first.pb"), filepath.Join(dir, "last.pb")
	auditWrite(t, first, auditMarshal(t, &names.NameDataset{Entries: []*names.NameEntry{{Name: "stale"}}}))
	auditWrite(t, last, []byte{0xff})
	l := New()
	if err := l.LoadSeparateFiles(first, last); err == nil {
		t.Fatal("malformed second dataset accepted")
	}
	if len(l.GetDataset().FirstNames) != 0 || len(l.GetDataset().LastNames) != 0 || l.IsLoaded() {
		t.Errorf("failed load changed visible data: %+v", l.GetStats())
	}
	auditWrite(t, first, auditMarshal(t, auditDataset().FirstNames))
	auditWrite(t, last, auditMarshal(t, auditDataset().LastNames))
	if err := l.LoadSeparateFiles(first, last); err != nil {
		t.Fatal(err)
	}
	if _, ok := l.GetDataset().FirstNames["STALE"]; ok {
		t.Error("successful retry retained data from failed attempt")
	}
}

func TestAuditEmbeddedLoader(t *testing.T) {
	old := EmbeddedData
	t.Cleanup(func() { EmbeddedData = old })
	EmbeddedData = auditGzip(t, auditMarshal(t, auditDataset()))
	l, err := NewWithEmbeddedData()
	if err != nil {
		t.Fatal(err)
	}
	auditLoaded(t, l)
	EmbeddedData = []byte{0xff}
	if _, err := NewWithEmbeddedData(); err == nil {
		t.Error("invalid embedded bytes accepted")
	}
}
