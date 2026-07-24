package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
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
	case "observation":
		runObservationServer()
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
	case "stubborn-tree":
		runStubbornTree()
	case "stubborn-grandchild":
		signal.Ignore(os.Interrupt, os.Signal(syscall.SIGTERM), os.Signal(syscall.SIGHUP))
		writeTreePID("grandchild", os.Getpid())
		for {
			time.Sleep(time.Hour)
		}
	default:
		os.Exit(9)
	}
}

func runStubbornTree() {
	signal.Ignore(os.Interrupt, os.Signal(syscall.SIGTERM), os.Signal(syscall.SIGHUP))
	writeTreePID("child", os.Getpid())
	grandchild := exec.Command(os.Args[0], "stubborn-grandchild")
	grandchild.Stdin = nil
	grandchild.Stdout = os.Stdout
	grandchild.Stderr = os.Stderr
	if err := grandchild.Start(); err != nil {
		os.Exit(11)
	}
	_, _ = io.WriteString(os.Stdout, "tree-ready\n")
	_, _ = io.Copy(io.Discard, os.Stdin)
	for {
		time.Sleep(time.Hour)
	}
}

func writeTreePID(name string, pid int) {
	dir := os.Getenv("MCP_GOVERNOR_TREE_PID_DIR")
	if dir == "" {
		return
	}
	if os.WriteFile(filepath.Join(dir, name), []byte(strconv.Itoa(pid)+"\n"), 0o600) != nil {
		os.Exit(12)
	}
}

func runObservationServer() {
	if pidDir := os.Getenv("MCP_GOVERNOR_E2E_PID_DIR"); pidDir != "" {
		pidPath := filepath.Join(pidDir, strconv.Itoa(os.Getpid()))
		if os.WriteFile(pidPath, nil, 0o600) != nil {
			os.Exit(10)
		}
	}
	type request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			RequestID json.RawMessage `json:"requestId"`
		} `json:"params"`
	}
	pending := make(map[string]json.RawMessage)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var message request
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			continue
		}
		switch message.Method {
		case "tools/call":
			pending[string(message.ID)] = append(json.RawMessage(nil), message.ID...)
		case "notifications/cancelled":
			delete(pending, string(message.Params.RequestID))
		}
		if len(pending) < 2 {
			continue
		}
		for key, id := range pending {
			_, _ = fmt.Fprintf(os.Stdout,
				`{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"SECRET-BODY"}]}}`+"\n", id)
			delete(pending, key)
		}
	}
}
