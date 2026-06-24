package main

import (
	"sort"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

// docFromHTML 是测试辅助：从 HTML 字符串构建 goquery 文档。
func docFromHTML(t *testing.T, html string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("解析 HTML 失败: %v", err)
	}
	return doc
}

func TestExtractLinks(t *testing.T) {
	html := `<html><body>
		<a href="/install">Install</a>
		<a href="https://docs.x.com/api">API</a>
		<a href="https://github.com/x/y">GitHub</a>
		<a href="#section">anchor</a>
		<a href="mailto:a@b.com">mail</a>
		<a href="javascript:void(0)">js</a>
		<a href="https://docs.x.com/report.pdf">PDF</a>
		<a href="/install">dup</a>
		</body></html>`
	doc := docFromHTML(t, html)
	seed := mustParseURL(t, "https://docs.x.com")

	links := ExtractLinks(doc, "https://docs.x.com", seed, false)
	sort.Strings(links)

	want := []string{"https://docs.x.com/api", "https://docs.x.com/install"}
	if len(links) != len(want) {
		t.Fatalf("得到 %d 个链接 %v，期望 %d 个 %v", len(links), links, len(want), want)
	}
	for i := range want {
		if links[i] != want[i] {
			t.Errorf("[%d] 得到 %q，期望 %q", i, links[i], want[i])
		}
	}
}

func TestExtractLinksBaseTag(t *testing.T) {
	html := `<html><head><base href="https://docs.x.com/sub/"></head><body>
		<a href="page">Page</a>
		</body></html>`
	doc := docFromHTML(t, html)
	seed := mustParseURL(t, "https://docs.x.com")

	links := ExtractLinks(doc, "https://docs.x.com", seed, false)

	want := []string{"https://docs.x.com/sub/page"}
	if len(links) != 1 || links[0] != want[0] {
		t.Errorf("得到 %v，期望 %v", links, want)
	}
}

func TestExtractLinksSubdomain(t *testing.T) {
	html := `<html><body>
		<a href="https://api.x.com/users">API subdomain</a>
		</body></html>`
	doc := docFromHTML(t, html)
	seed := mustParseURL(t, "https://docs.x.com")

	if got := ExtractLinks(doc, "https://docs.x.com", seed, false); len(got) != 0 {
		t.Errorf("默认不抓子域，得到 %v", got)
	}
	if got := ExtractLinks(doc, "https://docs.x.com", seed, true); len(got) != 1 {
		t.Errorf("允许子域应抓到 1 个，得到 %v", got)
	}
}
