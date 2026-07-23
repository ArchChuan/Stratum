package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	mode := "echo"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	switch mode {
	case "duplex":
		_, _ = io.WriteString(os.Stdout, "server-line\n")
		_, _ = io.Copy(io.Discard, os.Stdin)
	case "rpc":
		_, _ = io.WriteString(os.Stderr, "fake-server: rpc ready\n")
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if json.Unmarshal(scanner.Bytes(), &request) != nil || request.Method != "tools/call" {
				continue
			}
			_, _ = fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"ok"}]}}`+"\n", request.ID)
		}
	case "echo":
		_, _ = io.WriteString(os.Stderr, "fake-server: echo ready\n")
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "exit":
		return
	case "error":
		os.Exit(7)
	case "sleep":
		reader := bufio.NewReader(os.Stdin)
		_, _ = reader.ReadString('\n')
		time.Sleep(time.Hour)
	case "disconnect":
		reader := bufio.NewReader(os.Stdin)
		_, _ = reader.ReadString('\n')
		return
	case "env":
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(8)
		}
		_, _ = fmt.Fprintln(os.Stdout, os.Getenv("FAKE_VALUE"))
		_, _ = fmt.Fprintln(os.Stdout, cwd)
	default:
		os.Exit(9)
	}
}
