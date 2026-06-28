package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/time/rate"
)

// sitemap_crawler 实现导航驱动爬取的第二阶段（简化方案）：把第一阶段提取的导航菜单
// 树中每个带 URL 的节点视为一个独立抓取目标，**仅抓取该 URL 页面本身的正文**，绝不
// 提取页面中的任何链接做后续抓取——从源头杜绝「相关阅读 / 热门排行」污染。输出严格
// 按菜单树形结构组织目录与文件。
//
// 抓取目标先一次性从菜单树展开为「URL → 落盘路径列表」（同一 URL 可对应多个菜单路径，
// 仅抓取一次、在所有路径下落盘），再交由 worker pool 并发抓取，复用现有 Fetcher、
// 限流器与连续失败熔断。

// sitemapOptions 聚合 sitemap-crawl 子命令的全部参数。
type sitemapOptions struct {
	seed         string        // 入口 URL（方式 A：现场提取导航）
	navFile      string        // 第一阶段 nav.json 路径（方式 B：与 seed 二选一）
	navDepth     int           // 仅使用导航的前 N 层菜单作为抓取目标（0=全部）
	maxDepth     int           // 导航菜单最大层级，传给 ExtractNav（0=不限）
	selector     string        // 显式导航容器 CSS 选择器（仅方式 A）
	all          bool          // 保留站外导航链接（仅方式 A，默认仅同注册域）
	workers      int           // 并发 worker 数
	ratePerSec   float64       // 每秒最大请求数；<=0 表示不限流
	outDir       string        // Markdown 输出目录
	extract      options       // 单页提取参数（复用，保证提取行为一致）
	crawlTimeout time.Duration // 抓取级总超时；<=0 表示不限
}

// SitemapCrawler 驱动一次导航驱动抓取。
type SitemapCrawler struct {
	f       *Fetcher
	opts    sitemapOptions
	limiter *rate.Limiter
	logger  *log.Logger

	items []NavItem // 待抓取的导航菜单树

	// 抓取目标：orderedURLs 保持首次出现顺序；urlPaths 记录每个 URL 的全部落盘相对路径；
	// urlDepth 记录该 URL 在菜单树中的最浅层级（用于索引分组）；used 消解路径冲突。
	orderedURLs []string
	urlPaths    map[string][]string
	urlDepth    map[string]int
	used        map[string]bool
}

// NewSitemapCrawler 构造爬虫。ratePerSec>0 时建立 burst=1 的限流器（严格最小请求间隔）。
func NewSitemapCrawler(f *Fetcher, opts sitemapOptions) *SitemapCrawler {
	sc := &SitemapCrawler{
		f:        f,
		opts:     opts,
		logger:   log.New(os.Stderr, "webextract: ", log.LstdFlags),
		urlPaths: map[string][]string{},
		urlDepth: map[string]int{},
		used:     map[string]bool{},
	}
	if opts.ratePerSec > 0 {
		interval := time.Duration(float64(time.Second) / opts.ratePerSec)
		sc.limiter = rate.NewLimiter(rate.Every(interval), 1)
	}
	return sc
}

// sitemapResult 是单个 URL 抓取的结果（调度内部用）。
type sitemapResult struct {
	url      string
	finalURL string
	title    string
	status   string
	reason   string
	markdown string
}

// Run 执行导航驱动抓取：先获取导航菜单 → 展开为抓取目标 → worker pool 并发抓取 →
// 按菜单路径落盘。返回可被 output.go 复用的爬取报告。
func (sc *SitemapCrawler) Run(ctx context.Context) (*crawlReport, error) {
	seedNorm, err := sc.loadNav(ctx)
	if err != nil {
		return nil, err
	}

	report := &crawlReport{Seed: seedNorm, StartedAt: time.Now()}

	sc.collectTargets(sc.items, nil, 1)
	if len(sc.orderedURLs) == 0 {
		report.FinishedAt = time.Now()
		return report, fmt.Errorf("导航菜单中没有可抓取的 URL")
	}

	jobsCap := sc.opts.workers * 2
	resCap := sc.opts.workers*3 + 1 // > jobsCap + workers，避免取消时 worker 阻塞在 resCh
	jobsCh := make(chan string, jobsCap)
	resCh := make(chan sitemapResult, resCap)

	var wg sync.WaitGroup
	for i := 0; i < sc.opts.workers; i++ {
		wg.Add(1)
		go sc.worker(ctx, jobsCh, resCh, &wg)
	}

	queue := append([]string{}, sc.orderedURLs...)
	inFlight := 0
	consecFail := 0
	received := 0
	dispatchCapped := false
	var pages []crawlResult

	flush := func() {
		for len(queue) > 0 {
			select {
			case jobsCh <- queue[0]:
				queue = queue[1:]
				inFlight++
			default:
				return
			}
		}
	}

	aborted := false
	for (len(queue) > 0 && !dispatchCapped) || inFlight > 0 {
		if !dispatchCapped {
			flush()
		}
		select {
		case <-ctx.Done():
			aborted = true
		case r := <-resCh:
			inFlight--
			received++
			sc.handleResult(&r, &pages)
			switch r.status {
			case statusOK:
				consecFail = 0
			case statusSkip:
				// 取消跳过不计入熔断
			default:
				consecFail++
			}
			sc.logger.Printf("[%d/%d] %s %s", received, len(sc.orderedURLs), r.status, r.url)

			if consecFail >= maxConsecutiveFailures && !dispatchCapped {
				dispatchCapped = true
				queue = nil
				sc.logger.Printf("连续 %d 次失败，停止派发新任务（等待在途任务结束）", consecFail)
			}
		}
		if aborted {
			break
		}
	}

	close(jobsCh)
	wg.Wait()

	report.Pages = pages
	report.Stats = statsFromPages(pages)
	report.FinishedAt = time.Now()

	if aborted {
		return report, ctx.Err()
	}
	return report, nil
}

// loadNav 获取待抓取的导航菜单树，返回规范化后的入口 URL（供报告与索引使用）。
//   - 方式 B（--nav）：读取第一阶段 JSON 文件解析为 NavItem 树。
//   - 方式 A（<URL>）：渲染首页并调用 ExtractNav 现场提取导航菜单树。
func (sc *SitemapCrawler) loadNav(ctx context.Context) (string, error) {
	if sc.opts.navFile != "" {
		data, err := os.ReadFile(sc.opts.navFile)
		if err != nil {
			return "", fmt.Errorf("读取 nav 文件失败: %w", err)
		}
		var tree NavTree
		if err := json.Unmarshal(data, &tree); err != nil {
			return "", fmt.Errorf("解析 nav JSON 失败: %w", err)
		}
		sc.items = tree.Items
		return tree.Seed, nil
	}

	htmlStr, finalURL, err := sc.f.Fetch(sc.opts.seed, sc.opts.extract)
	if err != nil {
		return "", fmt.Errorf("抓取首页失败: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStr))
	if err != nil {
		return "", fmt.Errorf("解析 HTML 失败: %w", err)
	}
	seedNorm, err := NormalizeURL(finalURL)
	if err != nil {
		return "", fmt.Errorf("入口 URL 无效: %w", err)
	}
	seedURL, err := url.Parse(seedNorm)
	if err != nil {
		return "", fmt.Errorf("入口 URL 无效: %w", err)
	}
	sc.items = ExtractNav(doc, finalURL, seedURL, navOptions{
		maxDepth: sc.opts.maxDepth,
		selector: sc.opts.selector,
		all:      sc.opts.all,
	})
	return seedNorm, nil
}

// collectTargets 把菜单树递归展开为抓取目标。dirParts 是当前累积的目录段（已 sanitize），
// depth 为当前层级（从 1 起）。落盘规则（PRD 6.5）：
//   - 有子菜单的节点 → 作为一层目录；若同时带 URL，其正文写入该目录下 index.md。
//   - 无子菜单但带 URL 的叶子节点 → 写入 <菜单文本>.md。
//   - 无 URL 的分组标题节点 → 仅参与目录层级，不产生抓取任务。
//
// navDepth>0 时仅展开前 N 层菜单作为抓取目标。
func (sc *SitemapCrawler) collectTargets(items []NavItem, dirParts []string, depth int) {
	for _, item := range items {
		text := sanitizeSegment(item.Text)
		if text == "" {
			text = "untitled"
		}
		if len(item.Children) > 0 {
			childDir := append(append([]string{}, dirParts...), text)
			if item.URL != "" {
				rel := filepath.Join(append(append([]string{}, childDir...), "index"+mdExt)...)
				sc.addTarget(item.URL, rel, depth)
			}
			if sc.opts.navDepth == 0 || depth < sc.opts.navDepth {
				sc.collectTargets(item.Children, childDir, depth+1)
			}
			continue
		}
		if item.URL != "" {
			rel := filepath.Join(append(append([]string{}, dirParts...), text+mdExt)...)
			sc.addTarget(item.URL, rel, depth)
		}
	}
}

// addTarget 登记一个抓取目标：规范化 URL，消解落盘路径冲突，并记录 URL→路径映射。
// 同一 URL 多次出现时仅入队一次，但累积全部落盘路径。
func (sc *SitemapCrawler) addTarget(rawURL, rel string, depth int) {
	norm, err := NormalizeURL(rawURL)
	if err != nil {
		norm = rawURL
	}
	rel = resolveCollision(rel, sc.used)
	if _, ok := sc.urlPaths[norm]; !ok {
		sc.orderedURLs = append(sc.orderedURLs, norm)
		sc.urlDepth[norm] = depth - 1
	}
	sc.urlPaths[norm] = append(sc.urlPaths[norm], rel)
}

// worker 循环消费 URL：限流等待 → 抓取提取 → 回灌结果。jobsCh 关闭即退出。
func (sc *SitemapCrawler) worker(ctx context.Context, jobsCh <-chan string, resCh chan<- sitemapResult, wg *sync.WaitGroup) {
	defer wg.Done()
	for u := range jobsCh {
		if sc.limiter != nil {
			if err := sc.limiter.Wait(ctx); err != nil {
				resCh <- sitemapResult{url: u, status: statusSkip, reason: "canceled"}
				continue
			}
		}
		resCh <- sc.processOne(ctx, u)
	}
}

// processOne 抓取并处理单个 URL：Fetch → 提取标题 → extractFromHTML 提取正文 Markdown。
// 绝不提取页面中的链接做后续抓取。
func (sc *SitemapCrawler) processOne(ctx context.Context, u string) sitemapResult {
	res := sitemapResult{url: u}

	htmlStr, finalURL, err := sc.f.Fetch(u, sc.opts.extract)
	if err != nil {
		res.status = statusFail
		res.reason = clipReason(err.Error())
		return res
	}
	res.finalURL = finalURL

	if doc, derr := goquery.NewDocumentFromReader(strings.NewReader(htmlStr)); derr == nil {
		res.title = pageTitle(doc)
	}

	md, err := extractFromHTML(htmlStr, finalURL, sc.opts.extract)
	if err != nil {
		res.status = statusFail
		res.reason = "convert: " + clipReason(err.Error())
		return res
	}
	res.status = statusOK
	res.markdown = md
	return res
}

// handleResult 在主调度线程内处理单个结果：成功则把正文写入该 URL 对应的全部菜单路径，
// 每个落盘文件登记一条索引记录；失败则记录一条带失败原因的记录。
func (sc *SitemapCrawler) handleResult(r *sitemapResult, pages *[]crawlResult) {
	paths := sc.urlPaths[r.url]
	depth := sc.urlDepth[r.url]

	if r.status != statusOK || r.markdown == "" {
		*pages = append(*pages, crawlResult{
			URL:      r.url,
			FinalURL: r.finalURL,
			Title:    r.title,
			Depth:    depth,
			Status:   r.status,
			Reason:   r.reason,
		})
		return
	}

	for _, rel := range paths {
		n, werr := SavePage(sc.opts.outDir, rel, r.markdown)
		page := crawlResult{
			URL:      r.url,
			FinalURL: r.finalURL,
			Title:    r.title,
			Depth:    depth,
			Status:   statusOK,
			File:     rel,
			Bytes:    n,
		}
		if werr != nil {
			sc.logger.Printf("保存失败 %s → %s: %v", r.url, rel, werr)
			page.Status = statusFail
			page.Reason = "save: " + werr.Error()
			page.File = ""
			page.Bytes = 0
		}
		*pages = append(*pages, page)
	}
}

// statsFromPages 从落盘记录汇总统计：Total 为记录条数，按状态分类。
func statsFromPages(pages []crawlResult) crawlStats {
	s := crawlStats{}
	for _, p := range pages {
		s.Total++
		switch p.Status {
		case statusOK:
			s.Success++
		case statusSkip:
			s.Skipped++
		default:
			s.Failed++
		}
	}
	return s
}
