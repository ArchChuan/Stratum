package main

// Temporary stubs: Task 1's generate() already contains the TS call points,
// but the real implementations belong to Task 3 (this file is replaced in
// full by Task 3's tsgen.go). Stubs return empty output so the plugin
// protocol and Go emitter build and run from Task 1 onward.
func goFileName(protoPath string) string { return "" }

func tsFileName(protoPath string) string { return "" }

func tsFile(msgs []*message, protoPath string) []byte { return nil }
