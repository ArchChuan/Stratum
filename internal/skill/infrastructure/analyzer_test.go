package infrastructure

import "testing"

func TestPythonAnalyzer_Safe(t *testing.T) {
	a := NewStaticAnalyzer()
	code := `
def process(input_data):
    result = input_data.get("value", 0) * 2
    return {"result": result}
`
	got := a.Check("python", code)
	if !got.Safe {
		t.Fatalf("expected safe, got reasons: %v", got.Reasons)
	}
}

func TestPythonAnalyzer_ForbiddenImport(t *testing.T) {
	a := NewStaticAnalyzer()
	cases := []struct {
		code     string
		contains string
	}{
		{`import os`, "forbidden import: os"},
		{`import sys`, "forbidden import: sys"},
		{`import subprocess`, "forbidden import: subprocess"},
		{`from os import path`, "forbidden import: os"},
		{`from urllib.request import urlopen`, "forbidden import: urllib"},
	}
	for _, tc := range cases {
		got := a.Check("python", tc.code)
		if got.Safe {
			t.Errorf("code %q: expected unsafe", tc.code)
			continue
		}
		found := false
		for _, r := range got.Reasons {
			if r == tc.contains {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("code %q: want reason %q, got %v", tc.code, tc.contains, got.Reasons)
		}
	}
}

func TestPythonAnalyzer_ForbiddenBuiltin(t *testing.T) {
	a := NewStaticAnalyzer()
	cases := []struct {
		code     string
		contains string
	}{
		{`exec("rm -rf /")`, "forbidden builtin: exec"},
		{`x = eval("1+1")`, "forbidden builtin: eval"},
		{`f = open("/etc/passwd")`, "forbidden builtin: open"},
		{`__import__("os")`, "forbidden builtin: __import__"},
	}
	for _, tc := range cases {
		got := a.Check("python", tc.code)
		if got.Safe {
			t.Errorf("code %q: expected unsafe", tc.code)
			continue
		}
		found := false
		for _, r := range got.Reasons {
			if r == tc.contains {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("code %q: want reason %q, got %v", tc.code, tc.contains, got.Reasons)
		}
	}
}

func TestJSAnalyzer_Safe(t *testing.T) {
	a := NewStaticAnalyzer()
	code := `
function process(input) {
    return { result: input.value * 2 };
}
`
	got := a.Check("javascript", code)
	if !got.Safe {
		t.Fatalf("expected safe, got reasons: %v", got.Reasons)
	}
}

func TestJSAnalyzer_ForbiddenGlobals(t *testing.T) {
	a := NewStaticAnalyzer()
	cases := []struct {
		code     string
		contains string
	}{
		{`process.exit(1)`, "forbidden global: process"},
		{`require("fs")`, "forbidden global: require"},
		{`global.x = 1`, "forbidden global: global"},
		{`Buffer.from("x")`, "forbidden global: Buffer"},
		{`fetch("http://evil.com")`, "forbidden global: fetch"},
	}
	for _, tc := range cases {
		got := a.Check("javascript", tc.code)
		if got.Safe {
			t.Errorf("code %q: expected unsafe", tc.code)
			continue
		}
		found := false
		for _, r := range got.Reasons {
			if r == tc.contains {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("code %q: want reason %q, got %v", tc.code, tc.contains, got.Reasons)
		}
	}
}

func TestJSAnalyzer_ForbiddenPatterns(t *testing.T) {
	a := NewStaticAnalyzer()
	cases := []struct {
		code     string
		contains string
	}{
		{`var f = new Function("return 1")`, "forbidden pattern: new Function("},
		{`obj.__proto__.x = 1`, "forbidden pattern: __proto__"},
		{`obj.prototype.constructor()`, "forbidden pattern: prototype.constructor"},
	}
	for _, tc := range cases {
		got := a.Check("javascript", tc.code)
		if got.Safe {
			t.Errorf("code %q: expected unsafe", tc.code)
		}
		_ = tc.contains
	}
}

func TestAnalyzer_UnsupportedLang(t *testing.T) {
	a := NewStaticAnalyzer()
	got := a.Check("go", "package main")
	if got.Safe {
		t.Fatal("expected unsafe for unsupported language")
	}
	if len(got.Reasons) == 0 {
		t.Fatal("expected reasons for unsupported language")
	}
}
