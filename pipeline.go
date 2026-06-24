package main

import (
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// extract 是核心流程：渲染抓取 → 定位正文 → 清理噪声 → 规范化代码块 → 转 Markdown。
func extract(urlStr string, cfg options, f *Fetcher) (string, error) {
	htmlStr, finalURL, err := f.Fetch(urlStr, cfg)
	if err != nil {
		return "", fmt.Errorf("抓取页面失败: %w", err)
	}
	if cfg.rawHTML {
		return htmlStr, nil
	}
	return extractFromHTML(htmlStr, finalURL, cfg)
}

// extractFromHTML 把已抓取的 HTML 转换为 Markdown，是 extract 的「Fetch 之后」部分。
// 抽出此函数，使 crawl 模式能复用同一套正文提取与转换逻辑：同一份 HTML 既用于正文
// 提取（这里），也用于站内链接发现（在 crawl 中用 goquery 单独解析原始 DOM）。
// finalURL 用于可选的来源标注（cfg.includeURL）。
func extractFromHTML(htmlStr, finalURL string, cfg options) (string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStr))
	if err != nil {
		return "", fmt.Errorf("解析 HTML 失败: %w", err)
	}

	root := selectContent(doc, cfg.selector)
	preprocess(root)

	rootHTML, err := root.Html()
	if err != nil {
		return "", fmt.Errorf("序列化正文失败: %w", err)
	}

	mdStr, err := toMarkdown(rootHTML)
	if err != nil {
		return "", fmt.Errorf("转换为 Markdown 失败: %w", err)
	}
	mdStr = cleanup(mdStr)

	if cfg.includeURL {
		mdStr = fmt.Sprintf("<!-- source: %s -->\n", finalURL) + mdStr
	}
	return mdStr, nil
}

// pageTitle 从页面 DOM 中提取一个可读标题：优先正文首个 <h1>，回退到 <title>。
// 供 crawl 模式生成索引用；提取不到返回空字符串。
func pageTitle(doc *goquery.Document) string {
	if h1 := strings.TrimSpace(doc.Find("h1").First().Text()); h1 != "" {
		return h1
	}
	if t := strings.TrimSpace(doc.Find("title").First().Text()); t != "" {
		return t
	}
	return ""
}
