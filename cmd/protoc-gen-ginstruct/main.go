// Command protoc-gen-ginstruct generates plain Go DTO structs and TS
// interfaces from .proto contracts (standard protoc plugin protocol).
// Generated Go code carries the exact json field names declared in proto
// and encoding/json semantics — no protobuf runtime in generated output.
package main

import (
	"io"
	"os"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/pluginpb"
)

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(1)
	}
	var req pluginpb.CodeGeneratorRequest
	if err := proto.Unmarshal(data, &req); err != nil {
		os.Exit(1)
	}
	out, err := proto.Marshal(generate(&req))
	if err != nil {
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		os.Exit(1)
	}
}
