// 本文件验证厂商识别边界、映射优先级与日志，不接触上游对象。
package pricing

import (
	"fmt"
	"strings"
	"testing"
)

type testLogger struct{ infos, warns []string }

func (l *testLogger) Info(f string, a ...interface{}) {
	l.infos = append(l.infos, fmt.Sprintf(f, a...))
}
func (l *testLogger) Warn(f string, a ...interface{}) {
	l.warns = append(l.warns, fmt.Sprintf(f, a...))
}

func testFile(t *testing.T) PriceFile {
	t.Helper()
	f, e := parsePriceFile([]byte(validPrices))
	if e != nil {
		t.Fatal(e)
	}
	return f
}

func TestMatchVendors(t *testing.T) {
	for _, tt := range []struct {
		name, url     string
		custom, match bool
	}{
		{"https", "https://dashscope.aliyuncs.com/compatible-mode", true, true},
		{"no scheme", "dashscope.aliyuncs.com/v1", true, true},
		{"subdomain", "https://a.dashscope.aliyuncs.com", true, true},
		{"international", "https://dashscope-intl.aliyuncs.com", true, false},
		{"suffix attack", "https://evildashscope.aliyuncs.com", true, false},
		{"empty", "", true, false},
		{"built in", "https://dashscope.aliyuncs.com", false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			log := &testLogger{}
			m, u, e := matchVendors([]Provider{{Name: "OriginalName", Custom: tt.custom, BaseURL: tt.url}}, testFile(t), nil, log)
			if e != nil {
				t.Fatal(e)
			}
			if (len(m) == 1) != tt.match {
				t.Fatalf("matches=%v", m)
			}
			if tt.match && m[0].Provider != "OriginalName" {
				t.Fatal("provider normalized")
			}
			if (len(u) == 1) != (tt.custom && !tt.match) {
				t.Fatalf("unknown=%v", u)
			}
			if !tt.custom && len(log.infos) != 0 {
				t.Fatal("built in logged")
			}
			if len(u) > 0 && !strings.Contains(strings.Join(log.infos, " "), endpointHost(tt.url)) {
				t.Fatal("host missing")
			}
		})
	}
}

func TestVendorMap(t *testing.T) {
	f := testFile(t)
	mapping, e := ParseVendorMap("QwEn=dashscope,missing=dashscope,builtin=dashscope", f)
	if e != nil {
		t.Fatal(e)
	}
	if mapping["qwen"] != "dashscope" {
		t.Fatal(mapping)
	}
	log := &testLogger{}
	m, u, e := matchVendors([]Provider{{Name: "qwen", Custom: true, BaseURL: "https://proxy.invalid"}, {Name: "builtin"}}, f, mapping, log)
	if e != nil || len(m) != 1 || len(u) != 0 || len(log.warns) != 2 {
		t.Fatalf("%v %v %v %+v", m, u, e, log)
	}
	for _, raw := range []string{"qwen=unknown", "qwen", "=dashscope", "qwen=dashscope,QWEN=dashscope"} {
		if _, e := ParseVendorMap(raw, f); e == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	f.Vendors = append(f.Vendors, Vendor{ID: "other", EndpointHosts: []string{"aliyuncs.com"}})
	if _, _, e = matchVendors([]Provider{{Name: "q", Custom: true, BaseURL: "dashscope.aliyuncs.com"}}, f, nil, log); e == nil {
		t.Fatal("ambiguous match accepted")
	}
}
