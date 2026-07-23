//go:build ignore

package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

type tcpProxy struct {
	listener net.Listener
	target   string

	mu      sync.Mutex
	enabled bool
	closed  bool
	active  map[*proxyConnection]struct{}
}

type proxyConnection struct {
	proxy    *tcpProxy
	inbound  net.Conn
	outbound net.Conn
	once     sync.Once
}

func newTCPProxy(listener net.Listener, target string) *tcpProxy {
	return &tcpProxy{listener: listener, target: target, enabled: true, active: make(map[*proxyConnection]struct{})}
}

func (proxy *tcpProxy) serve() {
	for {
		inbound, err := proxy.listener.Accept()
		if err != nil {
			return
		}
		go proxy.forward(inbound)
	}
}

func (proxy *tcpProxy) forward(inbound net.Conn) {
	proxy.mu.Lock()
	enabled := proxy.enabled && !proxy.closed
	proxy.mu.Unlock()
	if !enabled {
		_ = inbound.Close()
		return
	}
	outbound, err := net.DialTimeout("tcp", proxy.target, 3*time.Second)
	if err != nil {
		_ = inbound.Close()
		return
	}
	connection := &proxyConnection{proxy: proxy, inbound: inbound, outbound: outbound}
	proxy.mu.Lock()
	if !proxy.enabled || proxy.closed {
		proxy.mu.Unlock()
		connection.close()
		return
	}
	proxy.active[connection] = struct{}{}
	proxy.mu.Unlock()
	go connection.copy(outbound, inbound)
	go connection.copy(inbound, outbound)
}

func (connection *proxyConnection) copy(destination, source net.Conn) {
	_, _ = io.Copy(destination, source)
	connection.close()
}

func (connection *proxyConnection) close() {
	connection.once.Do(func() {
		_ = connection.inbound.Close()
		_ = connection.outbound.Close()
		connection.proxy.mu.Lock()
		delete(connection.proxy.active, connection)
		connection.proxy.mu.Unlock()
	})
}

func (proxy *tcpProxy) setEnabled(enabled bool) {
	proxy.mu.Lock()
	proxy.enabled = enabled
	connections := make([]*proxyConnection, 0, len(proxy.active))
	if !enabled {
		for connection := range proxy.active {
			connections = append(connections, connection)
		}
	}
	proxy.mu.Unlock()
	for _, connection := range connections {
		connection.close()
	}
}

func (proxy *tcpProxy) state() (bool, int) {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	return proxy.enabled, len(proxy.active)
}

func (proxy *tcpProxy) close() {
	proxy.mu.Lock()
	proxy.closed = true
	proxy.enabled = false
	connections := make([]*proxyConnection, 0, len(proxy.active))
	for connection := range proxy.active {
		connections = append(connections, connection)
	}
	proxy.mu.Unlock()
	_ = proxy.listener.Close()
	for _, connection := range connections {
		connection.close()
	}
}

func main() {
	listener, err := net.Listen("tcp", requiredEnv("E2E_MILVUS_PROXY_ADDRESS"))
	if err != nil {
		panic("listen Milvus proxy failed")
	}
	proxy := newTCPProxy(listener, requiredEnv("E2E_MILVUS_PROXY_TARGET"))
	go proxy.serve()
	handler := http.NewServeMux()
	handler.HandleFunc("/mode", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var payload struct {
			Enabled bool `json:"enabled"`
		}
		if json.NewDecoder(io.LimitReader(request.Body, 1<<10)).Decode(&payload) != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		proxy.setEnabled(payload.Enabled)
		writer.WriteHeader(http.StatusNoContent)
	})
	handler.HandleFunc("/state", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		enabled, active := proxy.state()
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"enabled": enabled, "active_connections": active})
	})
	if err := http.ListenAndServe(requiredEnv("E2E_MILVUS_PROXY_CONTROL_ADDRESS"), handler); err != nil {
		panic("serve Milvus proxy control failed")
	}
}

func requiredEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		panic("required Milvus proxy environment is missing")
	}
	return value
}
