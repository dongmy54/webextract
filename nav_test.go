package main

import (
	"strings"
	"testing"
)

// findItem 在菜单项切片中按文本查找首个匹配项。
func findItem(items []NavItem, text string) *NavItem {
	for i := range items {
		if items[i].Text == text {
			return &items[i]
		}
	}
	return nil
}

// TestExtractNavBasic 覆盖标准 ul/li 结构与相对链接规范化。
func TestExtractNavBasic(t *testing.T) {
	html := `<html><body>
		<nav>
			<ul>
				<li><a href="/news">新闻</a></li>
				<li><a href="/tech">科技</a></li>
				<li><a href="/sports">体育</a></li>
			</ul>
		</nav>
	</body></html>`
	doc := docFromHTML(t, html)
	seed := mustParseURL(t, "https://example.com")

	items := ExtractNav(doc, "https://example.com", seed, navOptions{})
	if len(items) != 3 {
		t.Fatalf("期望 3 个顶层菜单项，得到 %d：%+v", len(items), items)
	}
	if got := findItem(items, "新闻"); got == nil || got.URL != "https://example.com/news" {
		t.Errorf("新闻项不正确：%+v", got)
	}
}

// TestExtractNavNested 覆盖二级子菜单嵌套，核心校验「本层链接不被子菜单链接干扰」。
func TestExtractNavNested(t *testing.T) {
	html := `<html><body>
		<header><nav>
			<ul>
				<li>
					<a href="/news">新闻</a>
					<ul>
						<li><a href="/news/domestic">国内</a></li>
						<li><a href="/news/world">国际</a></li>
					</ul>
				</li>
				<li><a href="/tech">科技</a></li>
			</ul>
		</nav></header>
	</body></html>`
	doc := docFromHTML(t, html)
	seed := mustParseURL(t, "https://example.com")

	items := ExtractNav(doc, "https://example.com", seed, navOptions{})
	news := findItem(items, "新闻")
	if news == nil {
		t.Fatalf("未找到「新闻」，得到 %+v", items)
	}
	if news.URL != "https://example.com/news" {
		t.Errorf("新闻主链接应为 /news，得到 %q", news.URL)
	}
	if len(news.Children) != 2 {
		t.Fatalf("新闻应有 2 个子项，得到 %d：%+v", len(news.Children), news.Children)
	}
	if c := findItem(news.Children, "国内"); c == nil || c.URL != "https://example.com/news/domestic" {
		t.Errorf("子项「国内」不正确：%+v", c)
	}
}

// TestExtractNavGroupTitle 覆盖无链接的分组标题作为父节点。
func TestExtractNavGroupTitle(t *testing.T) {
	html := `<html><body>
		<nav>
			<ul>
				<li><span>产品</span>
					<ul>
						<li><a href="/p/a">产品A</a></li>
						<li><a href="/p/b">产品B</a></li>
					</ul>
				</li>
			</ul>
		</nav>
	</body></html>`
	doc := docFromHTML(t, html)
	seed := mustParseURL(t, "https://example.com")

	items := ExtractNav(doc, "https://example.com", seed, navOptions{})
	if len(items) != 1 {
		t.Fatalf("期望 1 个分组，得到 %d：%+v", len(items), items)
	}
	g := items[0]
	if g.Text != "产品" || g.URL != "" {
		t.Errorf("分组标题应文本=产品、URL 为空，得到 %+v", g)
	}
	if len(g.Children) != 2 {
		t.Errorf("分组应有 2 个子项，得到 %d", len(g.Children))
	}
}

// TestExtractNavDedup 覆盖重复链接去重（保留首次出现）。
func TestExtractNavDedup(t *testing.T) {
	html := `<html><body>
		<nav>
			<ul>
				<li><a href="/news">新闻</a></li>
				<li><a href="/news">新闻镜像</a></li>
			</ul>
		</nav>
	</body></html>`
	doc := docFromHTML(t, html)
	seed := mustParseURL(t, "https://example.com")

	items := ExtractNav(doc, "https://example.com", seed, navOptions{})
	if len(items) != 1 {
		t.Fatalf("重复链接应去重为 1 项，得到 %d：%+v", len(items), items)
	}
	if items[0].Text != "新闻" {
		t.Errorf("应保留首次出现的「新闻」，得到 %q", items[0].Text)
	}
}

// TestExtractNavExternalFilter 覆盖站外链接过滤：默认清空 URL，--all 保留。
func TestExtractNavExternalFilter(t *testing.T) {
	html := `<html><body>
		<nav>
			<ul>
				<li><a href="/about">关于</a></li>
				<li><a href="https://twitter.com/x">微博</a></li>
			</ul>
		</nav>
	</body></html>`
	doc := docFromHTML(t, html)
	seed := mustParseURL(t, "https://example.com")

	items := ExtractNav(doc, "https://example.com", seed, navOptions{})
	weibo := findItem(items, "微博")
	if weibo == nil {
		t.Fatalf("应保留站外菜单项文本，得到 %+v", items)
	}
	if weibo.URL != "" {
		t.Errorf("默认应过滤站外 URL，得到 %q", weibo.URL)
	}

	allItems := ExtractNav(doc, "https://example.com", seed, navOptions{all: true})
	weiboAll := findItem(allItems, "微博")
	if weiboAll == nil || weiboAll.URL != "https://twitter.com/x" {
		t.Errorf("--all 应保留站外 URL，得到 %+v", weiboAll)
	}
}

// TestExtractNavFlatFallback 覆盖容器内无 ul/ol 时的扁平回退。
func TestExtractNavFlatFallback(t *testing.T) {
	html := `<html><body>
		<nav>
			<a href="/news">新闻</a>
			<a href="/tech">科技</a>
			<a href="javascript:void(0)">忽略</a>
			<a href="/doc.pdf">资源</a>
		</nav>
	</body></html>`
	doc := docFromHTML(t, html)
	seed := mustParseURL(t, "https://example.com")

	items := ExtractNav(doc, "https://example.com", seed, navOptions{})
	if len(items) != 2 {
		t.Fatalf("扁平回退应得 2 项（过滤 js/pdf），得到 %d：%+v", len(items), items)
	}
}

// TestExtractNavMaxDepth 覆盖 --max-depth 限制层级。
func TestExtractNavMaxDepth(t *testing.T) {
	html := `<html><body>
		<nav>
			<ul>
				<li><a href="/news">新闻</a>
					<ul><li><a href="/news/a">国内</a></li></ul>
				</li>
			</ul>
		</nav>
	</body></html>`
	doc := docFromHTML(t, html)
	seed := mustParseURL(t, "https://example.com")

	items := ExtractNav(doc, "https://example.com", seed, navOptions{maxDepth: 1})
	news := findItem(items, "新闻")
	if news == nil {
		t.Fatalf("未找到「新闻」")
	}
	if len(news.Children) != 0 {
		t.Errorf("max-depth=1 应不含子项，得到 %d", len(news.Children))
	}
}

// TestExtractNavSelectorOverride 覆盖显式 selector 覆盖自动检测。
func TestExtractNavSelectorOverride(t *testing.T) {
	html := `<html><body>
		<nav><ul><li><a href="/auto">自动</a></li></ul></nav>
		<div class="custom"><ul><li><a href="/manual">手动</a></li></ul></div>
	</body></html>`
	doc := docFromHTML(t, html)
	seed := mustParseURL(t, "https://example.com")

	items := ExtractNav(doc, "https://example.com", seed, navOptions{selector: ".custom"})
	if len(items) != 1 || items[0].Text != "手动" {
		t.Errorf("显式 selector 应只取 .custom，得到 %+v", items)
	}
}

// TestExtractNavMultiSection 覆盖文档侧边栏：单个容器内多个 <h3>+<ul> 分区，
// 且分组头用 <details><summary> 而非 <a>。回归「只取首个 ul 导致漏抓」的缺陷。
func TestExtractNavMultiSection(t *testing.T) {
	html := `<html><body>
		<nav>
			<div><h3>Getting Started</h3>
				<ul>
					<li><a href="/overview">Overview</a></li>
					<li><a href="/quickstart">Quickstart</a></li>
				</ul>
			</div>
			<div><h3>Using Codex</h3>
				<ul>
					<li><details><summary>App</summary>
						<ul>
							<li><a href="/app/overview">Overview</a></li>
							<li><a href="/app/features">Features</a></li>
						</ul>
					</details></li>
					<li><details><summary>CLI</summary>
						<ul><li><a href="/cli/overview">Overview</a></li></ul>
					</details></li>
				</ul>
			</div>
		</nav>
	</body></html>`
	doc := docFromHTML(t, html)
	seed := mustParseURL(t, "https://example.com")

	items := ExtractNav(doc, "https://example.com", seed, navOptions{})
	if len(items) != 2 {
		t.Fatalf("应得 2 个分区，得到 %d：%+v", len(items), items)
	}
	using := findItem(items, "Using Codex")
	if using == nil {
		t.Fatalf("第二个分区「Using Codex」漏抓，得到 %+v", items)
	}
	app := findItem(using.Children, "App")
	if app == nil || len(app.Children) != 2 {
		t.Fatalf("details/summary「App」未正确解析子项：%+v", app)
	}
	if c := findItem(app.Children, "Features"); c == nil || c.URL != "https://example.com/app/features" {
		t.Errorf("App 子项「Features」不正确：%+v", c)
	}
}

// TestRenderNavTree 覆盖树形渲染基本结构。
func TestRenderNavTree(t *testing.T) {
	tree := NavTree{
		Seed: "https://example.com",
		Items: []NavItem{
			{Text: "新闻", URL: "https://example.com/news", Children: []NavItem{
				{Text: "国内", URL: "https://example.com/news/a"},
			}},
			{Text: "科技", URL: "https://example.com/tech"},
		},
	}
	out := RenderNavTree(tree, true)
	if !strings.Contains(out, "新闻") || !strings.Contains(out, "https://example.com/news") {
		t.Errorf("树形输出缺少菜单名或 URL：\n%s", out)
	}
	if !strings.Contains(out, "国内") {
		t.Errorf("树形输出缺少子项：\n%s", out)
	}
}
