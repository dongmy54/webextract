package main

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

// nav 负责从已渲染的首页 DOM 中提取「主导航菜单树」，是导航驱动爬取的第一阶段。
//
// 关键点：导航普遍采用 <ul><li><a> 嵌套结构。对每个 <li>，先「克隆并删除其中的
// 子列表」取剩余首个 <a> 作为本层主链接，再在原始 <li> 下递归处理嵌套子列表，
// 从而把「本层菜单项的链接」与「子菜单里的链接」干净地分离。

// NavItem 是导航菜单树的一个节点。
type NavItem struct {
	Text     string    `json:"text"`               // 菜单文本（已去空白）
	URL      string    `json:"url,omitempty"`      // 规范化后的绝对 URL（无链接时为空，如分组标题）
	Children []NavItem `json:"children,omitempty"` // 子菜单
}

// NavTree 是 nav 命令的完整输出，JSON 形式即第二阶段的输入。
type NavTree struct {
	Seed  string    `json:"seed"`  // 入口 URL（规范化后）
	Items []NavItem `json:"items"` // 顶层菜单项
}

// navOptions 聚合导航提取的可调参数。
type navOptions struct {
	maxDepth int    // 菜单最大层级（1=仅一级，2=含二级，0=不限）
	selector string // 显式导航容器 CSS 选择器，覆盖自动检测
	all      bool   // 保留站外链接（默认仅保留同注册域链接）
}

// navCandidateSelectors 列出所有「可能是导航容器」的选择器。自动检测时收集全部命中
// 节点并打分（见 navScore），取得分最高者——这样文档站「链接最多、嵌套最深」的侧边栏
// 能胜出，而不会被顶栏少量链接的 header nav 抢先选中。
var navCandidateSelectors = []string{
	"nav",
	"[role=navigation]",
	"aside",
	".main-nav", ".main-menu", ".primary-menu", ".navbar", ".nav", ".menu",
	".sidebar", ".side-nav", ".docs-nav", ".doc-nav", ".toc",
	"#nav", "#navbar", "#menu", "#navigation", "#sidebar",
}

// ExtractNav 从渲染后的原始 DOM 中提取主导航菜单树。
//   - doc：用渲染后原始 HTML 构建的 goquery 文档
//   - baseURL：当前页面最终 URL（重定向后），相对链接解析基准
//   - seed：入口 URL，用于站内范围判定
//   - opts：导航提取参数
//
// 返回顶层菜单项列表（已规范化 URL、去重、按 opts 过滤）。
func ExtractNav(doc *goquery.Document, baseURL string, seed *url.URL, opts navOptions) []NavItem {
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

	container := locateNavContainer(doc, opts.selector)
	if container == nil || container.Length() == 0 {
		return nil
	}

	b := &navBuilder{base: base, seed: seed, opts: opts, seen: map[string]struct{}{}}
	items := b.buildFromContainer(container)
	return items
}

// locateNavContainer 定位主导航容器。显式 selector 优先；否则收集所有候选容器，按
// navScore 打分取最高者（兼顾链接数与嵌套层级）——文档站的左侧菜单通常因此胜出。
// 排除 footer 内导航与「包含其它候选容器」的外层包裹节点（避免选中把顶栏+侧栏一锅端的
// 大容器）。全部无果时兜底为 header 内含较多 <a> 的块。
func locateNavContainer(doc *goquery.Document, selector string) *goquery.Selection {
	if selector != "" {
		if sel := doc.Find(selector); sel.Length() > 0 {
			return sel.First()
		}
		return nil
	}

	// 收集去重后的候选节点。
	seen := map[*html.Node]bool{}
	var candidates []*goquery.Selection
	for _, s := range navCandidateSelectors {
		doc.Find(s).Each(func(_ int, node *goquery.Selection) {
			n := node.Get(0)
			if n == nil || seen[n] {
				return
			}
			seen[n] = true
			if node.Closest("footer").Length() > 0 {
				return // 排除 footer 内导航
			}
			if node.Find("a[href]").Length() == 0 {
				return
			}
			candidates = append(candidates, node)
		})
	}

	// 仅用「真正含链接的候选」作为包裹判定集合，避免一个 nav 因内嵌微小 .toc 而被误判为包裹层。
	candidateSet := map[*html.Node]bool{}
	for _, node := range candidates {
		if n := node.Get(0); n != nil {
			candidateSet[n] = true
		}
	}

	var best *goquery.Selection
	bestScore := 0
	for _, node := range candidates {
		// 跳过「包含其它候选容器」的外层包裹节点，优先更聚焦的内层导航。
		if containsOtherCandidate(node, candidateSet) {
			continue
		}
		if s := navScore(node); s > bestScore {
			bestScore = s
			best = node
		}
	}
	if best != nil {
		return best
	}
	// 兜底：header 内 <a> 最多的直接块。
	return fallbackHeaderBlock(doc)
}

// navScore 评估一个容器「像主导航」的程度：链接数为主，嵌套列表加权（结构化菜单优于
// 一排扁平链接）。
func navScore(sel *goquery.Selection) int {
	links := sel.Find("a[href]").Length()
	lists := sel.Find("ul, ol").Length()
	return links + 2*lists
}

// containsOtherCandidate 判断 node 内部是否还嵌套着其它候选导航节点。若是，说明它是更大的
// 包裹容器，应让位给更聚焦的内层导航。
func containsOtherCandidate(node *goquery.Selection, candidateSet map[*html.Node]bool) bool {
	inner := false
	node.Find("*").EachWithBreak(func(_ int, d *goquery.Selection) bool {
		if n := d.Get(0); n != nil && candidateSet[n] {
			inner = true
			return false
		}
		return true
	})
	return inner
}

// fallbackHeaderBlock 在 header 内寻找含 <a> 最多的容器作为导航兜底。
func fallbackHeaderBlock(doc *goquery.Document) *goquery.Selection {
	header := doc.Find("header").First()
	if header.Length() == 0 {
		return nil
	}
	if header.Find("a[href]").Length() == 0 {
		return nil
	}
	return header
}

// navBuilder 携带递归构建菜单树所需的共享状态。
type navBuilder struct {
	base *url.URL
	seed *url.URL
	opts navOptions
	seen map[string]struct{} // 已出现的规范化 URL，用于全局去重
}

// buildFromContainer 从导航容器构建顶层菜单项。容器内可能有「多个并列的顶层 ul/ol 分区」
// （如文档侧边栏按 h3 标题分组的多个区块），需全部处理而非只取首个。每个分区若有紧邻的
// 标题（h1-h6），则作为分组父节点包裹其菜单项；否则其菜单项直接并入顶层。
// 容器内若无任何 ul/ol，回退为「容器内顶层 <a href> 的扁平列表」。
func (b *navBuilder) buildFromContainer(container *goquery.Selection) []NavItem {
	lists := topLevelLists(container)
	if len(lists) == 0 {
		return b.buildFlat(container)
	}
	var items []NavItem
	for _, list := range lists {
		secItems := b.buildFromList(list, 1)
		if len(secItems) == 0 {
			continue
		}
		if heading := precedingHeading(list); heading != "" {
			items = append(items, NavItem{Text: heading, Children: secItems})
		} else {
			items = append(items, secItems...)
		}
	}
	return items
}

// topLevelLists 返回容器内所有「顶层」ul/ol，即其祖先链中（容器范围内）没有其它 ul/ol
// 的列表。这样多个并列分区都能被处理，而嵌套子列表交由 buildLi 递归。
func topLevelLists(container *goquery.Selection) []*goquery.Selection {
	var lists []*goquery.Selection
	container.Find("ul, ol").Each(func(_ int, list *goquery.Selection) {
		if list.Parent().Closest("ul, ol").Length() == 0 {
			lists = append(lists, list)
		}
	})
	return lists
}

// precedingHeading 查找列表所属分区的标题：取列表的前序兄弟中最近的 h1-h6；
// 若无，再尝试列表父节点的前序兄弟标题。返回去图标后的纯文本。
func precedingHeading(list *goquery.Selection) string {
	if h := list.PrevAll().Filter("h1, h2, h3, h4, h5, h6").First(); h.Length() > 0 {
		return headingText(h)
	}
	if h := list.Parent().PrevAll().Filter("h1, h2, h3, h4, h5, h6").First(); h.Length() > 0 {
		return headingText(h)
	}
	return ""
}

// headingText 提取标题文本并清理图标连字。
func headingText(h *goquery.Selection) string {
	hc := h.Clone()
	stripIconNodes(hc)
	return normalizeSpace(hc.Text())
}

// buildFromList 处理一个 ul/ol：遍历其直接 li 子项，递归构建。depth 为当前层级（从 1 起）。
func (b *navBuilder) buildFromList(list *goquery.Selection, depth int) []NavItem {
	var items []NavItem
	list.ChildrenFiltered("li").Each(func(_ int, li *goquery.Selection) {
		if item, ok := b.buildLi(li, depth); ok {
			items = append(items, item)
		}
	})
	return items
}

// buildLi 处理单个 li：克隆并删除子列表后取首个 <a> 作为主链接，再在原 li 下递归子列表。
// 返回的 ok=false 表示该项应被丢弃（既无文本也无有效链接与子项）。
func (b *navBuilder) buildLi(li *goquery.Selection, depth int) (NavItem, bool) {
	var item NavItem

	// 主链接：克隆 li → 删除其中所有子列表 → 取剩余首个 <a href>。
	clone := li.Clone()
	clone.Find("ul, ol").Remove()
	stripIconNodes(clone)
	anchor := clone.Find("a[href]").First()
	if anchor.Length() > 0 {
		item.Text = normalizeSpace(anchor.Text())
		if u, ok := b.resolveScoped(anchor.AttrOr("href", "")); ok {
			item.URL = u
		}
	}
	if item.Text == "" {
		// 无链接（如分组标题）：用克隆后剩余的直接文本。
		item.Text = normalizeSpace(clone.Text())
	}

	// 子菜单：在原始 li 下查找嵌套的 ul/ol 并递归（受 maxDepth 限制）。
	if b.opts.maxDepth == 0 || depth < b.opts.maxDepth {
		if sub := topLevelList(li); sub != nil && sub.Length() > 0 {
			item.Children = b.buildFromList(sub, depth+1)
		}
	}

	if item.URL == "" && item.Text == "" && len(item.Children) == 0 {
		return NavItem{}, false
	}
	// 去重：URL 已出现且无子项则丢弃；有子项则保留结构但清空重复 URL。
	if item.URL != "" {
		if _, dup := b.seen[item.URL]; dup {
			if len(item.Children) == 0 {
				return NavItem{}, false
			}
			item.URL = ""
		} else {
			b.seen[item.URL] = struct{}{}
		}
	}
	if item.Text == "" && item.URL == "" && len(item.Children) == 0 {
		return NavItem{}, false
	}
	return item, true
}

// buildFlat 回退策略：容器内无 ul/ol 时，把容器内所有 <a href> 作为扁平菜单项。
func (b *navBuilder) buildFlat(container *goquery.Selection) []NavItem {
	var items []NavItem
	container.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		ac := a.Clone()
		stripIconNodes(ac)
		text := normalizeSpace(ac.Text())
		u, ok := b.resolveScoped(a.AttrOr("href", ""))
		if !ok {
			return
		}
		if u == "" && text == "" {
			return
		}
		if u != "" {
			if _, dup := b.seen[u]; dup {
				return
			}
			b.seen[u] = struct{}{}
		}
		items = append(items, NavItem{Text: text, URL: u})
	})
	return items
}

// resolveScoped 解析单个 href 为规范化绝对 URL，并应用范围过滤。
// 返回 (url, ok)：
//   - ok=false：该链接应整体跳过（特殊协议 / 静态资源 / 解析失败）。
//   - ok=true 且 url=="":链接被站外过滤（非 --all 时），菜单项仍保留但无 URL。
func (b *navBuilder) resolveScoped(href string) (string, bool) {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") {
		return "", true // 空/纯锚点：保留菜单项文本，无 URL
	}
	low := strings.ToLower(href)
	if isSpecialScheme(low) {
		return "", false
	}
	ref, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	abs := b.base.ResolveReference(ref)
	if isAssetSuffix(abs.Path) {
		return "", false
	}
	normalized, err := NormalizeURL(abs.String())
	if err != nil {
		return "", false
	}
	nu, err := url.Parse(normalized)
	if err != nil {
		return "", false
	}
	if !b.opts.all && !InScope(nu, b.seed, false) {
		return "", true // 站外：保留文本，清空 URL
	}
	return normalized, true
}

// topLevelList 返回 sel 下「最靠近的」一层 ul/ol：优先直接子级，其次后代中第一个。
func topLevelList(sel *goquery.Selection) *goquery.Selection {
	if direct := sel.ChildrenFiltered("ul, ol"); direct.Length() > 0 {
		return direct.First()
	}
	if nested := sel.Find("ul, ol"); nested.Length() > 0 {
		return nested.First()
	}
	return nil
}

// stripIconNodes 从克隆的选区中移除常见图标节点，避免图标字体的连字文本
// （如 material-icons 的 "arrow_drop_down"）污染菜单文本。仅在文本提取前用于克隆体。
func stripIconNodes(sel *goquery.Selection) {
	sel.Find("svg, i, [class*=icon], [class*=Icon], [aria-hidden=true]").Remove()
}

// normalizeSpace 折叠所有连续空白为单个空格并去首尾空白。
func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// RenderNavTree 把菜单树渲染为人类可读的树形文本。includeURL 控制是否在每项后附 URL。
// 站外被过滤（URL 为空但有文本）的项标注「（站外，已过滤）」。
func RenderNavTree(tree NavTree, includeURL bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "网站导航 — %s\n\n", tree.Seed)
	for i, item := range tree.Items {
		renderNavItem(&sb, item, "", i == len(tree.Items)-1, includeURL)
	}
	return sb.String()
}

// renderNavItem 递归渲染单个菜单项及其子项。prefix 是父级累积的缩进，last 标记是否为同级末项。
func renderNavItem(sb *strings.Builder, item NavItem, prefix string, last, includeURL bool) {
	branch := "├─ "
	childPrefix := prefix + "│  "
	if last {
		branch = "└─ "
		childPrefix = prefix + "   "
	}

	label := item.Text
	if label == "" {
		label = "(无标题)"
	}
	switch {
	case item.URL != "" && includeURL:
		label += "  " + item.URL
	case item.URL == "" && item.Text != "" && len(item.Children) == 0:
		label += " （站外，已过滤）"
	}

	fmt.Fprintf(sb, "%s%s%s\n", prefix, branch, label)
	for i, child := range item.Children {
		renderNavItem(sb, child, childPrefix, i == len(item.Children)-1, includeURL)
	}
}
