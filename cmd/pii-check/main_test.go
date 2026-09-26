package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/montevive/go-name-detector/pkg/detector"
	"github.com/montevive/go-name-detector/pkg/loader"
	names "github.com/montevive/go-name-detector/pkg/proto"
	"github.com/montevive/go-name-detector/pkg/types"
	"google.golang.org/protobuf/proto"
)

func auditPIITemp(t *testing.T) string {
	t.Helper()
	base := os.Getenv("FLOWER_AUDIT_TMP")
	if base == "" {
		base = "../../../../../tmp"
	}
	dir, err := os.MkdirTemp(base, "pii-audit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func auditPIIFixture(t *testing.T) (*detector.Detector, string) {
	t.Helper()
	data, err := proto.Marshal(&names.CombinedNameDataset{
		FirstNames: &names.NameDataset{Entries: []*names.NameEntry{{Name: "Alpha", Rank: map[string]int32{"US": 1}, Country: map[string]float32{"US": 1}, Gender: map[string]float32{"M": 1}}}},
		LastNames:  &names.NameDataset{Entries: []*names.NameEntry{{Name: "Bravo", Rank: map[string]int32{"US": 2}, Country: map[string]float32{"US": 1}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(auditPIITemp(t), "names.pb")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	l := loader.New()
	if err := l.LoadFromBytes(data); err != nil {
		t.Fatal(err)
	}
	return detector.New(l.GetDataset()), path
}

func auditPIICapture(t *testing.T, f func()) (string, string) {
	t.Helper()
	dir := auditPIITemp(t)
	stdout, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	stderr, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdout, stderr
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	f()
	if _, err := stdout.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := stderr.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatal(err)
	}
	errors, err := io.ReadAll(stderr)
	if err != nil {
		t.Fatal(err)
	}
	return string(out), string(errors)
}

func auditPIIOptions(t *testing.T) {
	t.Helper()
	a, b, c, d, e, f := *dataPath, *threshold, *jsonOutput, *batch, *stats, *help
	t.Cleanup(func() { *dataPath, *threshold, *jsonOutput, *batch, *stats, *help = a, b, c, d, e, f })
	*threshold, *jsonOutput, *batch, *stats, *help = 0.7, false, "", false, false
}

func auditPIIMain(args ...string) {
	oldArgs, oldFlags := os.Args, flag.CommandLine
	defer func() { os.Args, flag.CommandLine = oldArgs, oldFlags }()
	os.Args = append([]string{"pii-check"}, args...)
	flag.CommandLine = flag.NewFlagSet("pii-check", flag.ExitOnError)
	main()
}

func TestAuditPIIMain(t *testing.T) {
	auditPIIOptions(t)
	_, path := auditPIIFixture(t)
	*dataPath = path
	for _, mode := range []string{"help", "stats", "human", "json", "batch"} {
		t.Run(mode, func(t *testing.T) {
			*help, *stats, *jsonOutput, *batch = mode == "help", mode == "stats", mode == "json", ""
			if mode == "batch" {
				*batch = filepath.Join(auditPIITemp(t), "batch")
				if err := os.WriteFile(*batch, []byte("Alpha Bravo\nAbsent Missing\nOne\n\na b c d e f g\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			out, errors := auditPIICapture(t, func() { auditPIIMain("Alpha", "Bravo") })
			switch mode {
			case "help":
				if !strings.Contains(out, "Usage:") {
					t.Error("help is missing")
				}
			case "stats":
				if !strings.Contains(out, "Total names: 2") {
					t.Errorf("statistics = %q", out)
				}
			case "human":
				if !strings.Contains(out, "Likely PII name") || !strings.Contains(out, "First names: Alpha") || !strings.Contains(out, "Predicted gender: Male") {
					t.Errorf("human output = %q", out)
				}
			case "json":
				var result types.PIIResult
				if err := json.Unmarshal([]byte(out), &result); err != nil || !result.IsLikelyName {
					t.Errorf("JSON result = %+v, %v", result, err)
				}
			case "batch":
				if !strings.Contains(errors, "2 processed, 1 detected") || !strings.Contains(out, "NOT_PII") {
					t.Errorf("batch output = %q, %q", out, errors)
				}
			}
		})
	}
	if !pathExists(path) || pathExists(filepath.Join(filepath.Dir(path), "missing")) {
		t.Error("path existence check failed")
	}
	for _, result := range []types.PIIResult{{}, {Details: types.NameDetails{Pattern: "invalid_length"}}} {
		out, _ := auditPIICapture(t, func() { outputHuman(result, nil) })
		if !strings.Contains(out, "Not a PII name") {
			t.Errorf("negative result = %q", out)
		}
	}
}

func TestAuditBatchJSON(t *testing.T) {
	auditPIIOptions(t)
	d, _ := auditPIIFixture(t)
	path := filepath.Join(auditPIITemp(t), "batch")
	if err := os.WriteFile(path, []byte("Alpha Bravo\nAbsent Missing\n"), 0600); err != nil {
		t.Fatal(err)
	}
	*jsonOutput = true
	out, _ := auditPIICapture(t, func() { processBatchFile(path, d) })
	dec := json.NewDecoder(strings.NewReader(out))
	for line := 1; line <= 2; line++ {
		var row struct {
			Line int `json:"line"`
		}
		if err := dec.Decode(&row); err != nil || row.Line != line {
			t.Fatalf("batch JSON is not parseable: line=%d, error=%v, output=%q", row.Line, err, out)
		}
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Errorf("unexpected trailing batch output: %v", err)
	}
}

func TestAuditEmptyBatch(t *testing.T) {
	auditPIIOptions(t)
	d, _ := auditPIIFixture(t)
	path := filepath.Join(auditPIITemp(t), "batch")
	if err := os.WriteFile(path, []byte("\nOne\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, errors := auditPIICapture(t, func() { processBatchFile(path, d) })
	if !strings.Contains(errors, "0 processed, 0 detected as PII (0.0%)") || strings.Contains(errors, "NaN") {
		t.Errorf("empty batch summary = %q", errors)
	}
}

func TestAuditPIIErrors(t *testing.T) {
	for _, mode := range []string{"dataset", "arguments", "open", "read", "json"} {
		args := []string{"-test.run=^TestAuditPIIChild$"}
		if f := flag.Lookup("test.gocoverdir"); f != nil && f.Value.String() != "" {
			args = append(args, "-test.gocoverdir="+f.Value.String())
		}
		cmd := exec.Command(os.Args[0], args...)
		cmd.Env = append(os.Environ(), "FLOWER_PII_CHILD="+mode, "FLOWER_PII_DIR="+auditPIITemp(t))
		out, err := cmd.CombinedOutput()
		if err == nil || bytes.Contains(out, []byte("panic:")) {
			t.Errorf("%s failure needs a controlled error: %v, %s", mode, err, out)
		}
	}
}

func TestAuditPIIChild(t *testing.T) {
	mode := os.Getenv("FLOWER_PII_CHILD")
	if mode == "" {
		return
	}
	auditPIIOptions(t)
	dir := os.Getenv("FLOWER_PII_DIR")
	*dataPath = filepath.Join(dir, "names.pb")
	if err := os.WriteFile(*dataPath, []byte{0x0a, 0, 0x12, 0}, 0600); err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "dataset":
		*dataPath = filepath.Join(dir, "missing")
		auditPIIMain("Alpha", "Bravo")
	case "arguments":
		auditPIIMain()
	case "open":
		processBatchFile(filepath.Join(dir, "missing"), nil)
	case "read":
		processBatchFile(dir, nil)
	case "json":
		outputJSON(types.PIIResult{Confidence: math.NaN()})
	}
}
