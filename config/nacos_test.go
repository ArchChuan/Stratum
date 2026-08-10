package config

import "testing"

func TestNacosSettingsServerAddresses(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		host   string
		port   uint64
		scheme string
	}{
		{name: "http with port", url: "http://nacos:8848", host: "nacos", port: 8848, scheme: "http"},
		{name: "no scheme defaults http", url: "nacos:8848", host: "nacos", port: 8848, scheme: "http"},
		{name: "host only defaults 8848", url: "nacos", host: "nacos", port: 8848, scheme: "http"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NacosSettings{URL: tc.url}
			host, port, scheme, err := s.ServerAddresses()
			if err != nil {
				t.Fatalf("ServerAddresses() error: %v", err)
			}
			if host != tc.host || port != tc.port || scheme != tc.scheme {
				t.Fatalf("got %s:%d(%s), want %s:%d(%s)", host, port, scheme, tc.host, tc.port, tc.scheme)
			}
		})
	}
}

func TestNacosSettingsServerAddressesInvalid(t *testing.T) {
	s := NacosSettings{URL: "http://host:notaport"}
	if _, _, _, err := s.ServerAddresses(); err == nil {
		t.Fatal("expected error for invalid port")
	}
}

// fakeNacosClient 用于 config 包内测试，不依赖真实 Nacos。
type fakeNacosClient struct {
	contents  map[string]string
	listeners map[string]func(string)
	err       error
}

func (f *fakeNacosClient) GetConfig(dataID string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.contents[dataID], nil
}

func (f *fakeNacosClient) Listen(dataID string, onChange func(string)) error {
	if f.err != nil {
		return f.err
	}
	f.listeners[dataID] = onChange
	return nil
}

func (f *fakeNacosClient) Close() error { return nil }
