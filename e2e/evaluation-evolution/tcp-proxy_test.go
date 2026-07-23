//go:build ignore

package main

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestTCPProxyDisableClosesActiveConnectionsAndRecovers(t *testing.T) {
	target := startEchoServer(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen proxy: %v", err)
	}
	proxy := newTCPProxy(listener, target)
	go proxy.serve()
	t.Cleanup(proxy.close)

	active := dialProxy(t, listener.Addr().String())
	assertEcho(t, active, "before-outage")
	proxy.setEnabled(false)
	_ = active.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := active.Read(make([]byte, 1)); err == nil {
		t.Fatal("active connection remained open after disabling proxy")
	}
	_ = active.Close()

	rejected := dialProxy(t, listener.Addr().String())
	_ = rejected.SetDeadline(time.Now().Add(time.Second))
	_, _ = rejected.Write([]byte("during-outage"))
	if _, err := rejected.Read(make([]byte, 1)); err == nil {
		t.Fatal("new connection was forwarded while proxy was disabled")
	}
	_ = rejected.Close()

	proxy.setEnabled(true)
	recovered := dialProxy(t, listener.Addr().String())
	defer recovered.Close()
	assertEcho(t, recovered, "after-recovery")
}

func startEchoServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo server: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer connection.Close()
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()
	return listener.Addr().String()
}

func dialProxy(t *testing.T, address string) net.Conn {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	return connection
}

func assertEcho(t *testing.T, connection net.Conn, value string) {
	t.Helper()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	if _, err := connection.Write([]byte(value)); err != nil {
		t.Fatalf("write proxy: %v", err)
	}
	buffer := make([]byte, len(value))
	if _, err := io.ReadFull(connection, buffer); err != nil {
		t.Fatalf("read proxy: %v", err)
	}
	if string(buffer) != value {
		t.Fatalf("echo = %q, want %q", buffer, value)
	}
}
