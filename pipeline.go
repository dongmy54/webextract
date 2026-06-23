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
