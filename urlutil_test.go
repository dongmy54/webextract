package main

import (
	"net/url"
	"testing"
)

// mustParseURL 是测试辅助：解析 URL，失败即终止。
func mustParseURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", s, err)
	}
	return u
}

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"去 fragment", "https://x.com/a#frag", "https://x.com/a", false},
		{"去 utm", "https://x.com/a?utm_source=foo&id=1", "https://x.com/a?id=1", false},
		{"去 fbclid", "https://x.com/a?fbclid=abc&id=1", "https://x.com/a?id=1", false},
		{"query 排序", "https://x.com/a?b=2&a=1", "https://x.com/a?a=1&b=2", false},
		{"https 默认端口", "https://x.com:443/a", "https://x.com/a", false},
		{"http 默认端口", "http://x.com:80/a", "http://x.com/a", false},
		{"非默认端口保留", "http://x.com:8080/a", "http://x.com:8080/a", false},
		{"路径规整", "https://x.com//a//b/../c/", "https://x.com/a/c", false},
		{"host 小写 path 保留大小写", "https://X.COM/Ab/Cd", "https://x.com/Ab/Cd", false},
		{"根路径", "https://x.com/", "https://x.com/", false},
		{"无路径补根", "https://x.com", "https://x.com/", false},
		{"trim 空白", "  https://x.com/a  ", "https://x.com/a", false},
		{"非 http 协议报错", "mailto:a@b.com", "", true},
		{"javascript 协议报错", "javascript:void(0)", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeURL(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错，得到 %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if got != tc.want {
				t.Errorf("得到 %q，期望 %q", got, tc.want)
			}
		})
	}
}

func TestInScope(t *testing.T) {
	seed := mustParseURL(t, "https://docs.example.com")
	cases := []struct {
		name   string
		target string
		sub    bool
		want   bool
	}{
		{"同 host", "https://docs.example.com/install", false, true},
		{"不同 host 排除", "https://github.com/x/y", false, false},
		{"子域 默认排除", "https://api.example.com", false, false},
		{"子域 允许", "https://api.example.com/x", true, true},
		{"同后缀不同域 排除", "https://evilexample.com", true, false},
		{"非 http 排除", "mailto:a@b.com", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := mustParseURL(t, tc.target)
			if got := InScope(u, seed, tc.sub); got != tc.want {
				t.Errorf("InScope(%q,%v)=%v，期望 %v", tc.target, tc.sub, got, tc.want)
			}
		})
	}
}
