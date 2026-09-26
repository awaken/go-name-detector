package build_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	names "github.com/montevive/go-name-detector/pkg/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func auditBuildTemp(t *testing.T) string {
	t.Helper()
	base := os.Getenv("FLOWER_AUDIT_TMP")
	if base == "" {
		base = "../../../tmp"
	}
	dir, err := os.MkdirTemp(base, "name-build-audit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestAuditGeneratedPackagePath(t *testing.T) {
	module := os.Getenv("FLOWER_AUDIT_PROTOBUF")
	if module == "" {
		t.Skip("set FLOWER_AUDIT_PROTOBUF to an installed protobuf source module")
	}
	dir := auditBuildTemp(t)
	tool := filepath.Join(dir, "protoc-gen-go")
	build := exec.Command("go", "build", "-o", tool, "./cmd/protoc-gen-go")
	build.Dir = module
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build installed generator: %v, %s", err, output)
	}
	output, err := exec.Command("make", "-n", "generate").CombinedOutput()
	if err != nil {
		t.Fatalf("inspect generate target: %v, %s", err, output)
	}
	var options []string
	out := "."
	for _, field := range strings.Fields(string(output)) {
		if strings.HasPrefix(field, "--go_opt=") {
			options = append(options, strings.TrimPrefix(field, "--go_opt="))
		}
		if strings.HasPrefix(field, "--go_out=") {
			out = strings.TrimPrefix(field, "--go_out=")
		}
	}
	schema, err := os.ReadFile("proto/names.proto")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`option go_package\s*=\s*"([^"]+)"`).FindSubmatch(schema)
	if len(match) != 2 {
		t.Fatal("schema Go package option missing")
	}
	desc := protodesc.ToFileDescriptorProto(names.File_proto_names_proto)
	desc.Options.GoPackage = proto.String(string(match[1]))
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{desc.GetName()}, Parameter: proto.String(strings.Join(options, ",")),
		ProtoFile: []*descriptorpb.FileDescriptorProto{desc},
	}
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(tool)
	cmd.Stdin = bytes.NewReader(data)
	generated, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var response pluginpb.CodeGeneratorResponse
	if err := proto.Unmarshal(generated, &response); err != nil || response.GetError() != "" {
		t.Fatalf("generator response: %v, %s", err, response.GetError())
	}
	if len(response.File) != 1 {
		t.Fatalf("generated %d files, want one", len(response.File))
	}
	got := filepath.ToSlash(filepath.Join(out, response.File[0].GetName()))
	if got != "pkg/proto/names.pb.go" {
		t.Errorf("make generate writes %q; loader imports pkg/proto, which make clean removes", got)
	}
}

func TestAuditProtoPackageCoexist(t *testing.T) {
	mod := os.Getenv("FLOWER_AUDIT_MOD")
	if mod == "" {
		t.Skip("set FLOWER_AUDIT_MOD to the offline dependency modfile")
	}
	dir := auditBuildTemp(t)
	path := filepath.Join(dir, "main.go")
	program := "package main\nimport (\n_ \"github.com/montevive/go-name-detector/pkg/proto\"\n_ \"github.com/montevive/go-name-detector/proto\"\n)\nfunc main() {}\n"
	if err := os.WriteFile(path, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("go", "run", "-mod=mod", "-modfile="+mod, path).CombinedOutput()
	if err != nil {
		t.Errorf("the two published protobuf packages cannot coexist: %v, %s", err, output)
	}
}
