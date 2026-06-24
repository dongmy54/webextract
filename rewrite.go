package main

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// rewrite 把 Markdown 中的站内链接改写为指向本地文件的相对路径，使抓取下来的
// 文件结构自身支持点击跳转。
//
// 例：overview.md 中 [快速入门](/docs/zh-CN/quickstart) → [快速入门](quickstart.md)
//
// 改写规则：
//   - 仅改写本站内、且目标已被抓取（命中 urlToFile）的链接
//   - 命中：href 改写为「当前文件目录 → 目标文件」的相对路径，保留页内锚点(#xxx)
//   - 外部链接、纯锚点、特殊协议、未抓取目标：原样保留
//   - 代码块（``` / ~~~）内部的链接不改写

// mdLinkRe 匹配 Markdown 链接 [label](href) 或 [label](href "title")。
// 捕获组 1=label，组 2=href。
var mdLinkRe = regexp.MustCompile(`\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)

// RewriteLocalLinks 改写 Markdown 正文中的站内链接。
//   - sourceURL：当前页面最终 URL，用于把相对/绝对路径 href 解析为绝对 URL
//   - currentRel：当前文件相对输出目录的路径（如 docs/zh-CN/overview.md）
//   - urlToFile：规范化 URL → 目标文件相对路径 的映射（仅含已抓取页面）
//   - seed/allowSubdomains：判定链接是否属于本站
func RewriteLocalLinks(content, sourceURL, currentRel string, urlToFile map[string]string, seed *url.URL, allowSubdomains bool) string {
	source, err := url.Parse(sourceURL)
	if err != nil || source == nil || len(urlToFile) == 0 {
		return content
	}
	currentDir := filepath.ToSlash(filepath.Dir(currentRel))

	lines := strings.Split(content, "\n")
	inCode := false
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		lines[i] = mdLinkRe.ReplaceAllStringFunc(line, func(m string) string {
			return rewriteOneLink(m, source, currentDir, urlToFile, seed, allowSubdomains)
		})
	}
	return strings.Join(lines, "\n")
}

// rewriteOneLink 改写单个匹配到的 Markdown 链接；无需改写时原样返回。
func rewriteOneLink(matched string, source *url.URL, currentDir string, urlToFile map[string]string, seed *url.URL, allowSubdomains bool) string {
	sub := mdLinkRe.FindStringSubmatch(matched)
	if len(sub) < 3 {
		return matched
	}
	label, href := sub[1], sub[2]
	newHref := rewriteHref(href, source, currentDir, urlToFile, seed, allowSubdomains)
	if newHref == href {
		return matched
	}
	return fmt.Sprintf("[%s](%s)", label, newHref)
}

// rewriteHref 把单个 href 解析、规范化并查映射，命中则返回相对路径，否则原样返回。
func rewriteHref(href string, source *url.URL, currentDir string, urlToFile map[string]string, seed *url.URL, allowSubdomains bool) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") {
		return href // 纯锚点
	}
	if isSpecialScheme(strings.ToLower(href)) {
		return href // mailto/tel/javascript/data 等
	}
	ref, err := url.Parse(href)
	if err != nil {
		return href
	}
	abs := source.ResolveReference(ref)

	// 非本站链接（含外部绝对 URL）不改写。
	if !InScope(abs, seed, allowSubdomains) {
		return href
	}

	frag := abs.Fragment
	// 规范化（去 fragment、去 tracking 参数等）后查映射。
	canonical, err := NormalizeURL(abs.String())
	if err != nil {
		return href
	}
	target, ok := urlToFile[canonical]
	if !ok {
		return href // 目标未被抓取，保留原样
	}

	rel, err := filepath.Rel(currentDir, target)
	if err != nil {
		return href
	}
	rel = filepath.ToSlash(rel)
	if frag != "" {
		rel += "#" + frag
	}
	return rel
}
