package main

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// urlutil 提供 URL 规范化与站内过滤，是爬虫「去重」与「范围控制」的唯一权威来源。
// 所有进入任务队列的链接都先经过 NormalizeURL，保证 /a 与 /a/ 与 /a?utm=x 被归一。

// NormalizeURL 把任意 URL 规范化为一个稳定字符串，用于去重与作为实际抓取地址。
//
// 规范化内容：
//   - 仅接受 http/https（其余协议返回错误，由调用方跳过）
//   - host 小写、去掉 scheme 默认端口（:80/:443）
//   - 去除 fragment（#xxx）
//   - 路径规整：合并重复斜杠、消解 . 与 ..、去掉末尾斜杠（根 / 除外）
//   - 剔除常见 Tracking 参数（utm_*、fbclid 等），保留其余 query 并按键排序
//
// 注意：scheme 不强制归一为 https——保留原样，避免对 http-only 站点发起错误的
// https 请求（chromedp 会跟随 301 重定向，两阶段去重足以覆盖 http↔https 等价）。
func NormalizeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("解析 URL 失败: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("非 http(s) 协议: %q", u.Scheme)
	}

	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if port := u.Port(); port != "" {
		// 去掉 scheme 默认端口，保留非默认端口。
		if !((u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443")) {
			host = host + ":" + port
		}
	}
	u.Host = host

	u.Fragment = ""
	u.RawFragment = ""
	u.Path = cleanURLPath(u.Path)
	u.RawPath = "" // 规整后无需原始转义形式
	u.RawQuery = stripTrackingParams(u.Query()).Encode()
	u.User = nil // 去掉嵌入的用户信息（http://user:pass@host）

	return u.String(), nil
}

// cleanURLPath 规整 URL 路径：合并斜杠、消解 . 与 ..、去末尾斜杠，根路径恒为 "/"。
func cleanURLPath(p string) string {
	if p == "" {
		return "/"
	}
	c := path.Clean(p)
	if c == "." || c == ".." || c == "" {
		return "/"
	}
	if !strings.HasPrefix(c, "/") {
		c = "/" + c
	}
	return c
}

// stripTrackingParams 剔除 URL 中的 Tracking/统计参数，其余参数原样保留。
func stripTrackingParams(values url.Values) url.Values {
	out := make(url.Values, len(values))
	for k, vs := range values {
		if isTrackingParam(k) {
			continue
		}
		out[k] = vs
	}
	return out
}

// isTrackingParam 判断单个 query 键是否属于 Tracking 参数。
func isTrackingParam(k string) bool {
	lk := strings.ToLower(k)
	if strings.HasPrefix(lk, "utm_") {
		return true
	}
	switch lk {
	case "fbclid", "gclid", "dclid", "mc_cid", "mc_eid",
		"_ga", "_gl", "ref", "ref_src", "igshid", "yclid", "msclkid":
		return true
	}
	return false
}

// InScope 判断目标 URL 是否在爬取范围内。
//   - 仅 http/https
//   - 默认要求与入口同 host；allowSubdomains 为 true 时放宽到同注册域（基于 publicsuffix）
func InScope(u, seed *url.URL, allowSubdomains bool) bool {
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if isSameHost(u, seed) {
		return true
	}
	if allowSubdomains {
		return sameRegistrableDomain(hostname(u), hostname(seed))
	}
	return false
}

// isSameHost 比较 host（忽略大小写）。
func isSameHost(u, base *url.URL) bool {
	return hostname(u) == hostname(base)
}

func hostname(u *url.URL) string {
	return strings.ToLower(u.Hostname())
}

// sameRegistrableDomain 判断两个 host 是否属于同一可注册域（如 docs.example.com
// 与 api.example.com 同属 example.com）。基于 publicsuffix 精确判断，避免
// evilexample.com 这类同后缀但不同域的误判。
func sameRegistrableDomain(host1, baseHost string) bool {
	if host1 == "" || baseHost == "" {
		return false
	}
	d1, err := publicsuffix.EffectiveTLDPlusOne(host1)
	if err != nil {
		return false
	}
	d2, err := publicsuffix.EffectiveTLDPlusOne(baseHost)
	if err != nil {
		return false
	}
	return d1 == d2
}
