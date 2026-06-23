package main

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

// contentSelectors 按优先级列出常见的正文容器选择器。
var contentSelectors = []string{
	"main",
	"article",
	"[role=main]",
	"#content",
	"#content-container",
	"#main-content",
	"#__main",
	".markdown-body",
	".markdown",
	".prose",
}

// selectContent 定位页面正文区域：优先使用显式选择器，否则按常见容器逐个尝试，
// 多个匹配时取文本量最大的一个（避开侧栏/页脚的同名容器）。
func selectContent(doc *goquery.Document, selector string) *goquery.Selection {
	if selector != "" {
		if s := doc.Find(selector); s.Length() > 0 {
			return pickLargest(s)
		}
	}
	for _, sel := range contentSelectors {
		if s := doc.Find(sel); s.Length() > 0 {
			return pickLargest(s)
		}
	}
	return doc.Find("body")
}

func pickLargest(s *goquery.Selection) *goquery.Selection {
	var best *goquery.Selection
	bestLen := -1
	s.Each(func(_ int, el *goquery.Selection) {
		if n := len(el.Text()); n > bestLen {
			bestLen = n
			best = el
		}
	})
	if best == nil {
		return s.First()
	}
	return best
}

// noiseSelectors 列出非正文的元素：导航、侧栏、页脚、脚本样式、目录、复制按钮等。
var noiseSelectors = []string{
	"script", "style", "noscript", "template", "link", "meta",
	"iframe", "object", "embed", "svg", "canvas",
	"nav", "aside", "footer",
	"[role=navigation]", "[role=banner]", "[role=search]", "[role=contentinfo]",
	"button", "form", "input", "select",
	".toc-root", ".toc", ".table-of-contents", ".on-this-page",
	".fern-sidebar", ".fern-layout-footer", ".fern-toc",
	".theme-doc-sidebar", ".theme-doc-toc", ".theme-doc-footer",
	".pagination-nav", ".breadcrumbs", ".edit-this-page",
	".anchor", ".hash-link", ".copy-button", ".clipboard-button",
}

// removeNoise 从正文区域内移除非正文元素。
func removeNoise(root *goquery.Selection) {
	for _, sel := range noiseSelectors {
		root.Find(sel).Remove()
	}
}

// dataAsTags 把 Mintlify 等框架用 data-as 标注的语义元素映射为真实 HTML 标签。
// 例如 <span data-as="p"> 实际是段落，但默认会被当成行内元素，导致段落粘连。
var dataAsTags = map[string]string{
	"p":  "p",
	"h1": "h1", "h2": "h2", "h3": "h3", "h4": "h4", "h5": "h5", "h6": "h6",
	"ul": "ul", "ol": "ol", "li": "li",
	"blockquote": "blockquote", "pre": "pre", "code": "code",
	"hr": "hr", "br": "br",
	"strong": "strong", "em": "em", "b": "b", "i": "i",
	"table": "table", "thead": "thead", "tbody": "tbody",
	"tr": "tr", "th": "th", "td": "td",
}

// normalizeSemantic 将 data-as 标注的元素还原为真实标签，恢复其语义。
func normalizeSemantic(root *goquery.Selection) {
	root.Find("[data-as]").Each(func(_ int, s *goquery.Selection) {
		as, _ := s.Attr("data-as")
		tag, ok := dataAsTags[as]
		if !ok || len(s.Nodes) == 0 {
			return
		}
		if node := s.Nodes[0]; node.Type == html.ElementNode {
			node.Data = tag
		}
	})
}

// isEmptyish 判断字符串是否仅由空白与零宽/不可见字符组成（零宽字符以 \u 转义比较，
// 避免在源码中出现字面 BOM 等非法字符）。
func isEmptyish(s string) bool {
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r',
			'\u00A0', '\u200B', '\u200C', '\u200D',
			'\u2060', '\uFEFF', '\u1680', '\u3000':
			continue
		default:
			return false
		}
	}
	return true
}

// removeEmptyAnchors 移除标题中仅含零宽/空白字符的锚点链接（如 [​](#section)）。
func removeEmptyAnchors(root *goquery.Selection) {
	root.Find("a").Each(func(_ int, s *goquery.Selection) {
		href, ok := s.Attr("href")
		if !ok || !strings.HasPrefix(href, "#") {
			return
		}
		if isEmptyish(s.Text()) {
			s.Remove()
		}
	})
}

// langAliases 把各代码框架的语言标识归一化为通用形式（与多数文档习惯一致）。
var langAliases = map[string]string{
	"shellscript": "bash",
	"shell":       "bash",
	"sh":          "bash",
	"zsh":         "bash",
	"bat":         "batch",
	"cmd":         "batch",
	"py":          "python",
	"py3":         "python",
	"js":          "javascript",
	"ts":          "typescript",
	"golang":      "go",
	"yml":         "yaml",
	"tf":          "hcl",
}

func mapLang(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	if l == "" {
		return ""
	}
	if v, ok := langAliases[l]; ok {
		return v
	}
	return l
}

// preprocess 在转换为 Markdown 前对正文 DOM 做全部规范化处理。
func preprocess(root *goquery.Selection) {
	removeNoise(root)
	normalizeSemantic(root)
	removeEmptyAnchors(root)
	normalizeCodeBlocks(root)
}

// normalizeCodeBlocks 规范化代码块：先用行结构重建纯文本（剥离语法高亮 span 与行号），
// 再把语言标记统一写到 <code class="language-X">。
func normalizeCodeBlocks(root *goquery.Selection) {
	root.Find("pre").Each(func(_ int, pre *goquery.Selection) {
		if pre.ParentsFiltered("pre").Length() > 0 {
			return // 跳过嵌套的 pre
		}
		lang := mapLang(findLanguageFor(pre))

		// 优先基于行结构重建纯文本（适配 Fern 的 <table> 行 + 行号结构）。
		if text, ok := extractCodeLines(pre); ok {
			text = strings.TrimSpace(text)
			codeClass := "code"
			if lang != "" {
				codeClass = "language-" + lang
			}
			pre.SetHtml(`<code class="` + codeClass + `">` + html.EscapeString(text) + `</code>`)
			return
		}

		if lang == "" {
			return
		}
		code := pre.Find("code")
		if code.Length() == 0 {
			inner, _ := pre.Html()
			pre.SetHtml(`<code class="language-` + lang + `">` + inner + `</code>`)
		} else {
			code.First().SetAttr("class", "language-"+lang)
		}
	})
}

// extractCodeLines 针对 Fern 框架以表格行渲染的代码块，逐行提取内容单元格文本，
// 用换行连接，从而丢弃行号并恢复多行结构。无此结构时返回 ok=false。
func extractCodeLines(pre *goquery.Selection) (string, bool) {
	contents := pre.Find("td.code-block-line-content")
	if contents.Length() == 0 {
		return "", false
	}
	parts := make([]string, 0, contents.Length())
	contents.Each(func(_ int, c *goquery.Selection) {
		parts = append(parts, strings.TrimSpace(c.Text()))
	})
	return strings.Join(parts, "\n"), true
}

// findLanguageFor 在 pre 自身、内部 code 或最近的祖先上查找语言标记。
// 适配 Fern（class 含 language-X）与 Mintlify（language 属性）等框架。
func findLanguageFor(pre *goquery.Selection) string {
	if l := classLanguage(pre); l != "" {
		return l
	}
	if l := classLanguage(pre.Find("code")); l != "" {
		return l
	}
	node := pre.Parent()
	for i := 0; i < 6 && node.Length() > 0; i++ {
		if l := classLanguage(node); l != "" {
			return l
		}
		if l, ok := node.Attr("language"); ok && l != "" {
			return l
		}
		if l, ok := node.Attr("data-language"); ok && l != "" {
			return l
		}
		node = node.Parent()
	}
	return ""
}

func classLanguage(s *goquery.Selection) string {
	if s.Length() == 0 {
		return ""
	}
	cls, _ := s.Attr("class")
	for _, c := range strings.Fields(cls) {
		c = strings.TrimSpace(c)
		if strings.HasPrefix(c, "language-") {
			return strings.TrimPrefix(c, "language-")
		}
		if strings.HasPrefix(c, "lang-") {
			return strings.TrimPrefix(c, "lang-")
		}
	}
	return ""
}
