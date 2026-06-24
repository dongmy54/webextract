package main

import (
	"net/url"
	"path"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// links 负责从已渲染的页面 DOM 中发现站内可抓取链接。
//
// 关键点：必须在 selectContent/preprocess（噪音清理）之前，用渲染后的「原始 DOM」
// 提取——否则 nav/aside/footer 等侧栏导航（文档站的主要链接来源）会被删除，导致漏抓。

// mediaAndAssetSuffixes 列出非 HTML 资源后缀。抓取目标是网页正文，用 chromedp
// 渲染这些二进制/静态资源无意义且浪费，默认排除。
var mediaAndAssetSuffixes = map[string]bool{
	".pdf": true, ".zip": true, ".gz": true, ".tar": true, ".rar": true, ".7z": true,
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".svg": true,
	".webp": true, ".bmp": true, ".ico": true, ".tiff": true,
	".mp3": true, ".mp4": true, ".wav": true, ".webm": true, ".mov": true,
	".avi": true, ".mkv": true, ".flv": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
	".exe": true, ".dmg": true, ".iso": true, ".apk": true, ".deb": true, ".rpm": true,
	".css": true, ".js": true, ".mjs": true, ".map": true,
	".json": true, ".xml": true, ".rss": true, ".atom": true,
	".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
}

// ExtractLinks 从渲染后的原始 DOM 中提取站内可抓取链接。
//   - doc：用渲染后原始 HTML 构建的 goquery 文档（噪音清理之前）
//   - baseURL：当前页面最终 URL（重定向后），相对链接解析基准之一
//   - seed：入口 URL，用于站内范围判定
//   - allowSubdomains：是否允许同注册域的子域
//
// 处理：<base href> 优先作为基准；跳过纯锚点/特殊协议（javascript/mailto/tel/data/blob）；
// 跳过静态资源后缀；ResolveReference 解析相对/绝对链接；NormalizeURL 规范化；
// InScope 站内过滤；去重。返回规范化绝对 URL 列表。
func ExtractLinks(doc *goquery.Document, baseURL string, seed *url.URL, allowSubdomains bool) []string {
	base, err := url.Parse(baseURL)
	if err != nil || base == nil {
		return nil
	}
	// 页面若有 <base href>，以其作为相对链接解析基准。
	if bh, ok := doc.Find("base[href]").First().Attr("href"); ok && bh != "" {
		if ref, err := url.Parse(bh); err == nil {
			base = base.ResolveReference(ref)
		}
	}

	seen := make(map[string]struct{})
	var links []string
	add := func(href string) {
		href = strings.TrimSpace(href)
		if href == "" || strings.HasPrefix(href, "#") {
			return
		}
		low := strings.ToLower(href)
		if isSpecialScheme(low) {
			return
		}
		ref, err := url.Parse(href)
		if err != nil {
			return
		}
		abs := base.ResolveReference(ref)
		if isAssetSuffix(abs.Path) {
			return
		}
		normalized, err := NormalizeURL(abs.String())
		if err != nil {
			return
		}
		nu, err := url.Parse(normalized)
		if err != nil {
			return
		}
		if !InScope(nu, seed, allowSubdomains) {
			return
		}
		if _, dup := seen[normalized]; dup {
			return
		}
		seen[normalized] = struct{}{}
		links = append(links, normalized)
	}

	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) { add(s.AttrOr("href", "")) })
	doc.Find("area[href]").Each(func(_ int, s *goquery.Selection) { add(s.AttrOr("href", "")) })

	return links
}

// isSpecialScheme 判断是否为非网页协议前缀。
func isSpecialScheme(low string) bool {
	return strings.HasPrefix(low, "javascript:") ||
		strings.HasPrefix(low, "mailto:") ||
		strings.HasPrefix(low, "tel:") ||
		strings.HasPrefix(low, "data:") ||
		strings.HasPrefix(low, "blob:") ||
		strings.HasPrefix(low, "about:")
}

// isAssetSuffix 判断 URL 路径是否指向静态/二进制资源。
func isAssetSuffix(p string) bool {
	return mediaAndAssetSuffixes[strings.ToLower(path.Ext(p))]
}
