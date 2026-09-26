//go:build linux

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/montevive/go-name-detector/pkg/loader"
	names "github.com/montevive/go-name-detector/pkg/proto"
	"github.com/montevive/go-name-detector/pkg/types"
	"google.golang.org/protobuf/proto"
)

func auditBloomTemp(t *testing.T) string {
	t.Helper()
	base := os.Getenv("FLOWER_AUDIT_TMP")
	if base == "" {
		base = "../../../../../tmp"
	}
	dir, err := os.MkdirTemp(base, "bloom-audit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func auditBloomData(t *testing.T) {
	t.Helper()
	data, err := proto.Marshal(&names.CombinedNameDataset{
		FirstNames: &names.NameDataset{Entries: []*names.NameEntry{{Name: "Alpha", Rank: map[string]int32{"US": 1}}}},
		LastNames:  &names.NameDataset{Entries: []*names.NameEntry{{Name: "Bravo", Rank: map[string]int32{"US": 2}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	old := loader.EmbeddedData
	loader.EmbeddedData = data
	t.Cleanup(func() { loader.EmbeddedData = old })
}

func auditBloomMain(args []string) {
	oldArgs, oldFlags := os.Args, flag.CommandLine
	defer func() { os.Args, flag.CommandLine = oldArgs, oldFlags }()
	os.Args = append([]string{"bloomexport"}, args...)
	flag.CommandLine = flag.NewFlagSet("bloomexport", flag.ExitOnError)
	main()
}

func TestAuditBloomRoundTrip(t *testing.T) {
	auditBloomData(t)
	dir := auditBloomTemp(t)
	for _, role := range []string{"first", "last"} {
		path := filepath.Join(dir, role)
		auditBloomMain([]string{"-names", role, "-out", path})
		data, err := os.ReadFile(path)
		if err != nil || len(data) < 17 || string(data[:4]) != "NBF1" {
			t.Fatalf("invalid filter: %x, %v", data, err)
		}
		k, m := binary.LittleEndian.Uint32(data[4:8]), binary.LittleEndian.Uint64(data[8:16])
		if k != 8 || m != 11 || len(data)-16 != int((m+7)/8) {
			t.Errorf("incorrect filter header: k=%d, m=%d, bytes=%d", k, m, len(data))
		}
		key := "alpha"
		if role == "last" {
			key = "bravo"
		}
		h := fnv1a32(key)
		a, b := fmix32(h), fmix32(h^0x5bd1e995)|1
		for i := uint32(0); i < k; i++ {
			bit := (uint64(a) + uint64(i)*uint64(b)) % m
			if data[16+bit/8]&(1<<(bit%8)) == 0 {
				t.Errorf("export omitted selected name %q", key)
			}
		}
	}
	filter := newBloom(3, 11, 8)
	if fp := expectedFP(3, filter); math.IsNaN(fp) || fp <= 0 || fp >= 1 {
		t.Errorf("false-positive estimate = %v", fp)
	}
	if err := filter.write(dir); err == nil {
		t.Error("directory accepted as output file")
	}
	for _, tc := range []struct {
		text string
		want uint32
	}{{"", 2166136261}, {"a", 0xe40c292c}, {"foobar", 0xbf9cf968}} {
		if got := fnv1a32(tc.text); got != tc.want {
			t.Errorf("FNV-1a(%q) = %x, want %x", tc.text, got, tc.want)
		}
	}
}

func TestAuditBloomSelection(t *testing.T) {
	source := map[string]*types.NameData{
		"A": {}, "Alpha": {Rank: map[string]int32{"US": 1}},
		"Beta": {Rank: map[string]int32{"US": 11}}, "Gamma": {Rank: map[string]int32{"IT": 1}},
		"Absent": {Rank: map[string]int32{"US": 0}},
	}
	keys := selectNames(source, map[string]bool{"US": true}, 10)
	if len(keys) != 1 || !keys["alpha"] {
		t.Errorf("rank/country selection = %v", keys)
	}
	if keys := selectNames(source, nil, 0); len(keys) != 4 {
		t.Errorf("unfiltered selection = %v", keys)
	}
	if got := fold(" José "); got != "jose" {
		t.Errorf("fold = %q", got)
	}
}

func TestAuditBloomUnicodeContract(t *testing.T) {
	// NFD leaves Hangul as Jamo letters; none are combining marks to remove.
	if got := fold("한"); got != "\u1112\u1161\u11ab" {
		t.Errorf("NBF1 documented lookup key = %q, want decomposed Hangul", got)
	}
}

func auditBloomChild(t *testing.T, spec string) ([]byte, error) {
	t.Helper()
	args := []string{"-test.run=^TestAuditBloomChild$"}
	if f := flag.Lookup("test.gocoverdir"); f != nil && f.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+f.Value.String())
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "FLOWER_BLOOM_CHILD="+spec)
	return cmd.CombinedOutput()
}

func TestAuditBloomInvalidFlags(t *testing.T) {
	dir := auditBloomTemp(t)
	for _, args := range [][]string{
		{"-bits-per-entry", "0"}, {"-bits-per-entry", "1e308"}, {"-bits-per-entry", "18446744073709551616"},
		{"-k", "0"}, {"-k", "4294967296"}, {"-max-rank", "4294967296"},
		{"-names", "invalid"}, {"-countries", "XX"}, {"-out", dir}, {"-audit-bad-data"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			path := filepath.Join(dir, "filter")
			full := append([]string{"-out", path}, args...)
			spec, err := json.Marshal(full)
			if err != nil {
				t.Fatal(err)
			}
			output, err := auditBloomChild(t, string(spec))
			if err == nil || bytes.Contains(output, []byte("panic:")) {
				t.Errorf("invalid flags need a controlled error; error=%v, output=%s", err, output)
			}
			if _, err := os.Stat(path); err == nil {
				t.Error("invalid options produced an output artifact")
			}
			os.Remove(path)
		})
	}
}

func TestAuditBloomWriteFailures(t *testing.T) {
	for _, limit := range []string{"0", "4", "8", "16"} {
		if output, err := auditBloomChild(t, "limit:"+limit); err != nil {
			t.Errorf("write failure at %s bytes: %v, %s", limit, err, output)
		}
	}
}

func TestAuditBloomChild(t *testing.T) {
	spec := os.Getenv("FLOWER_BLOOM_CHILD")
	if spec == "" {
		return
	}
	if strings.HasPrefix(spec, "limit:") {
		dir := auditBloomTemp(t)
		var limit uint64
		if err := json.Unmarshal([]byte(strings.TrimPrefix(spec, "limit:")), &limit); err != nil {
			t.Fatal(err)
		}
		var previous syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &previous); err != nil {
			t.Fatal(err)
		}
		signal.Ignore(syscall.SIGXFSZ)
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: limit, Max: previous.Max}); err != nil {
			t.Fatal(err)
		}
		err := newBloom(2, 11, 8).write(filepath.Join(dir, "limited"))
		if restore := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &previous); restore != nil {
			t.Fatal(restore)
		}
		if err == nil {
			t.Error("file-size limit failure was lost")
		}
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(spec), &args); err != nil {
		t.Fatal(err)
	}
	auditBloomData(t)
	if args[len(args)-1] == "-audit-bad-data" {
		loader.EmbeddedData = []byte{0xff}
		args = args[:len(args)-1]
	}
	auditBloomMain(args)
}
