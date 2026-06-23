package main

import (
	"regexp"
	"strings"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/JohannesKaufmann/html-to-markdown/plugin"
)

// toMarkdown 把正文 HTML 转换为 GitHub Flavored Markdown。
// domain 传空字符串，使相对链接与绝对链接均按页面原样保留（与文档源一致）。
func toMarkdown(htmlStr string) (string, error) {
	conv := md.NewConverter("", true, nil)
	conv.Use(plugin.GitHubFlavored())
	return conv.ConvertString(htmlStr)
}

var (
	reMultipleBlank  = regexp.MustCompile(`\n{3,}`)
	reCodeFenceStart = regexp.MustCompile("(```[^\\n]*)\\n{2,}")
	reCodeFenceEnd   = regexp.MustCompile("\\n{2,}(```)")
	reHeadingSpace   = regexp.MustCompile(`(^|\n)(#{1,6})[ \t]{2,}`)
	reInlineCode     = regexp.MustCompile("`[^`\n]*`")
	reWordUnderscore = regexp.MustCompile(`([A-Za-z0-9])\\_([A-Za-z0-9])`)
)

// cleanup 规整输出：统一换行、去除行尾空白、还原被过度转义的连字符、
// 转义表格单元格内的管道符、清理代码围栏多余空行、规范标题前导空格、
// 压缩多余空行、移除页眉面包屑与文末反馈区、修剪首尾。所有规整均跳过代码块内部。
func cleanup(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	lines := strings.Split(s, "\n")
	inCode := false
	for i := range lines {
		raw := lines[i]
		trimmed := strings.TrimLeft(raw, " \t")
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inCode = !inCode
			continue
		}
		lines[i] = strings.TrimRight(raw, " \t")
		if inCode {
			continue
		}

		line := lines[i]
		// 标题记号后的多余空格（来自被移除的锚点链接）
		line = reHeadingSpace.ReplaceAllString(line, "$1$2 ")
		// 还原被过度转义的行内连字符（如 "\- " -> "- "）
		line = strings.ReplaceAll(line, ` \- `, ` - `)
		// 表格数据行：转义行内代码中的管道符，避免破坏列结构
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "|") {
			if !isTableSeparator(line) {
				line = normalizeCodeSpans(line)
				line = reWordUnderscore.ReplaceAllString(line, "${1}_${2}")
			}
		}
		lines[i] = line
	}
	s = strings.Join(lines, "\n")

	// 代码围栏紧邻的多余空行
	s = reCodeFenceStart.ReplaceAllString(s, "$1\n")
	s = reCodeFenceEnd.ReplaceAllString(s, "\n$1")
	// 压缩 3 行及以上空行为单个空行
	s = reMultipleBlank.ReplaceAllString(s, "\n\n")

	// 移除首个一级标题之前的页眉/面包屑，以及文末的页面反馈区
	s = trimLeadingBreadcrumb(s)
	s = trimTailNoise(s)

	return strings.TrimSpace(s)
}

// isTableSeparator 判断一行是否为表格的分隔行（如 | --- | :---: |）。
func isTableSeparator(line string) bool {
	for _, r := range line {
		if r != '|' && r != '-' && r != ':' && r != ' ' && r != '\t' {
			return false
		}
	}
	return strings.Contains(line, "-") && strings.Contains(line, "|")
}

// normalizeCodeSpans 规整表格单元格中的行内代码片段：把 | 转义为 \|（避免破坏
// 列结构），并还原被表格插件过度转义的下划线 \_。
func normalizeCodeSpans(line string) string {
	return reInlineCode.ReplaceAllStringFunc(line, func(m string) string {
		m = strings.ReplaceAll(m, "|", `\|`)
		m = strings.ReplaceAll(m, `\_`, `_`)
		return m
	})
}

// trimLeadingBreadcrumb 移除首个一级标题（行首 "# "）之前的内容（通常是页眉、
// 面包屑、logo 文本等非正文元素）。仅当该标题出现在开头 300 字节内时才处理，
// 以避免误删以大段引言开头的正文。
func trimLeadingBreadcrumb(s string) string {
	const maxLead = 300
	if strings.HasPrefix(s, "# ") {
		return s
	}
	pos := strings.Index(s, "\n# ")
	if pos < 0 || pos > maxLead {
		return s
	}
	return s[pos+1:]
}

// tailNoiseMarkers 列出常见的页面反馈区起始短语。命中任一则截断其后全部内容
// （反馈按钮、下一篇导航、快捷键提示等页脚元素）。
var tailNoiseMarkers = []string{
	"Was this page helpful",
	"Was this document helpful",
	"Did you find this page helpful",
}

func trimTailNoise(s string) string {
	cut := -1
	for _, m := range tailNoiseMarkers {
		if idx := strings.Index(s, m); idx >= 0 && (cut < 0 || idx < cut) {
			cut = idx
		}
	}
	if cut < 0 {
		return s
	}
	return strings.TrimRight(s[:cut], " \t\n")
}
