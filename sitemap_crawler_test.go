package main

import (
	"sort"
	"testing"
)

// newTestSitemapCrawler 构造一个仅用于目标展开测试的爬虫（无 Fetcher）。
func newTestSitemapCrawler(opts sitemapOptions) *SitemapCrawler {
	return &SitemapCrawler{
		opts:     opts,
		urlPaths: map[string][]string{},
		urlDepth: map[string]int{},
		used:     map[string]bool{},
	}
}

// TestCollectTargetsTreeLayout 覆盖菜单树 → 落盘路径的核心规则：
//   - 无子菜单叶子 → <文本>.md
//   - 有子菜单且带 URL → <文本>/index.md，子项落入同名目录
//   - 无 URL 的分组标题 → 仅作目录层级
func TestCollectTargetsTreeLayout(t *testing.T) {
	items := []NavItem{
		{Text: "新闻", URL: "https://example.com/news", Children: []NavItem{
			{Text: "国内", URL: "https://example.com/news/domestic"},
			{Text: "国际", URL: "https://example.com/news/world"},
		}},
		{Text: "科技", URL: "https://example.com/tech"},
		{Text: "分组", Children: []NavItem{ // 无 URL 的分组标题
			{Text: "子页", URL: "https://example.com/group/sub"},
		}},
	}
	sc := newTestSitemapCrawler(sitemapOptions{})
	sc.collectTargets(items, nil, 1)

	want := map[string]string{
		"https://example.com/news":          "新闻/index.md",
		"https://example.com/news/domestic": "新闻/国内.md",
		"https://example.com/news/world":    "新闻/国际.md",
		"https://example.com/tech":          "科技.md",
		"https://example.com/group/sub":     "分组/子页.md",
	}
	if len(sc.orderedURLs) != len(want) {
		t.Fatalf("目标数量 = %d，期望 %d；orderedURLs=%v", len(sc.orderedURLs), len(want), sc.orderedURLs)
	}
	for u, rel := range want {
		paths := sc.urlPaths[u]
		if len(paths) != 1 || paths[0] != rel {
			t.Errorf("URL %s 落盘路径 = %v，期望 [%s]", u, paths, rel)
		}
	}
}

// TestCollectTargetsDedup 同一 URL 出现在多个菜单路径下时仅抓取一次，但保留全部落盘路径。
func TestCollectTargetsDedup(t *testing.T) {
	items := []NavItem{
		{Text: "首页", URL: "https://example.com/"},
		{Text: "关于", URL: "https://example.com/"}, // 指向同一 URL
	}
	sc := newTestSitemapCrawler(sitemapOptions{})
	sc.collectTargets(items, nil, 1)

	if len(sc.orderedURLs) != 1 {
		t.Fatalf("去重后 URL 数量 = %d，期望 1", len(sc.orderedURLs))
	}
	paths := sc.urlPaths["https://example.com/"]
	sort.Strings(paths)
	want := []string{"关于.md", "首页.md"}
	if len(paths) != 2 || paths[0] != want[0] || paths[1] != want[1] {
		t.Errorf("落盘路径 = %v，期望 %v", paths, want)
	}
}

// TestCollectTargetsNavDepth navDepth 限制仅展开前 N 层菜单作为抓取目标。
func TestCollectTargetsNavDepth(t *testing.T) {
	items := []NavItem{
		{Text: "一级", URL: "https://example.com/a", Children: []NavItem{
			{Text: "二级", URL: "https://example.com/a/b"},
		}},
	}
	sc := newTestSitemapCrawler(sitemapOptions{navDepth: 1})
	sc.collectTargets(items, nil, 1)

	if _, ok := sc.urlPaths["https://example.com/a/b"]; ok {
		t.Errorf("navDepth=1 不应展开二级菜单，却命中 %s", "https://example.com/a/b")
	}
	if _, ok := sc.urlPaths["https://example.com/a"]; !ok {
		t.Errorf("navDepth=1 应保留一级菜单目标")
	}
}

// TestCollectTargetsCollision 不同 URL 落到同名路径时追加后缀消解冲突。
func TestCollectTargetsCollision(t *testing.T) {
	items := []NavItem{
		{Text: "页面", URL: "https://example.com/x"},
		{Text: "页面", URL: "https://example.com/y"}, // 同文本不同 URL
	}
	sc := newTestSitemapCrawler(sitemapOptions{})
	sc.collectTargets(items, nil, 1)

	all := append([]string{}, sc.urlPaths["https://example.com/x"]...)
	all = append(all, sc.urlPaths["https://example.com/y"]...)
	if len(all) != 2 || all[0] == all[1] {
		t.Errorf("冲突路径未消解：%v", all)
	}
}
