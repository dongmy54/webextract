package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/time/rate"
)

// crawler 实现 BFS 站点爬取：主调度 goroutine 独占所有可变状态（visited 去重集合、
// 待办队列、派发计数、文件名占用表、统计），worker pool 只负责「限流等待 → 抓取 →
// 提取 md+title+links」，结果通过 resCh 回灌。该结构无锁、易推理，是正确的关键。

// crawlOptions 聚合 crawl 子命令的全部参数。
type crawlOptions struct {
	seed            string        // 入口 URL（原始字符串）
	depth           int           // 最大深度（种子=0，抓取深度 0..depth）
	maxPages        int           // 最大抓取页数（含种子）
	workers         int           // 并发 worker 数（= 最大并发 tab 数）
	ratePerSec      float64       // 每秒最大请求数；<=0 表示不限流
	outDir          string        // Markdown 输出目录
	allowSubdomains bool          // 允许同注册域的子域
	extract         options       // 单页提取参数（复用，保证提取行为一致）
	crawlTimeout    time.Duration // 爬取级总超时；<=0 表示不限
}

const (
	statusOK   = "ok"     // 抓取并落盘成功
	statusFail = "error"  // 抓取/解析/转换/保存失败
	statusSkip = "skipped" // 因取消等跳过

	// 连续失败达到该阈值则停止派发（疑似 Chrome 主进程异常）。
	maxConsecutiveFailures = 10
	// 索引中失败原因的最大长度。
	maxReasonLen = 200
)

// crawlResult 是单个页面的处理结果。markdown 与 Links 仅用于调度，不写入索引 JSON。
type crawlResult struct {
	URL      string   `json:"url"`              // 规范化原始 URL（任务地址）
	FinalURL string   `json:"final_url"`        // 重定向后实际 URL
	Title    string   `json:"title,omitempty"`  // 页面标题
	Depth    int      `json:"depth"`            // 抓取深度（种子=0）
	Status   string   `json:"status"`           // ok / error / skipped
	Reason   string   `json:"reason,omitempty"` // 失败原因
	File     string   `json:"file,omitempty"`   // 落盘相对路径
	Bytes    int      `json:"bytes,omitempty"`  // Markdown 字节数

	Links    []string `json:"-"` // 发现的站内链接（调度用，不入索引）
	markdown string   // 提取的 Markdown（落盘用，不进 JSON）
}

// crawlStats 是爬取统计。
type crawlStats struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// crawlReport 是一次完整爬取的报告，序列化为 index.json。
type crawlReport struct {
	Seed       string        `json:"seed"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
	Stats      crawlStats    `json:"stats"`
	Pages      []crawlResult `json:"pages"`
}

// Duration 返回爬取总耗时（未结束时为 0）。
func (r *crawlReport) Duration() time.Duration {
	if r.FinishedAt.IsZero() || r.StartedAt.IsZero() {
		return 0
	}
	return r.FinishedAt.Sub(r.StartedAt)
}

// Crawler 驱动一次站点爬取。
type Crawler struct {
	f       *Fetcher
	opts    crawlOptions
	limiter *rate.Limiter
	logger  *log.Logger
	seed    *url.URL // 规范化后的入口 URL（Run 时设置，供 worker 做 InScope）
}

// NewCrawler 构造爬虫。ratePerSec>0 时建立 burst=1 的限流器（严格最小请求间隔）。
func NewCrawler(f *Fetcher, opts crawlOptions) *Crawler {
	c := &Crawler{
		f:      f,
		opts:   opts,
		logger: log.New(os.Stderr, "webextract: ", log.LstdFlags),
	}
	if opts.ratePerSec > 0 {
		interval := time.Duration(float64(time.Second) / opts.ratePerSec)
		c.limiter = rate.NewLimiter(rate.Every(interval), 1)
	}
	return c
}

// Run 执行 BFS 爬取，返回完整报告。种子本身抓取失败时返回 error（无种子则无后续）。
func (c *Crawler) Run(ctx context.Context) (*crawlReport, error) {
	seedNorm, err := NormalizeURL(c.opts.seed)
	if err != nil {
		return nil, fmt.Errorf("入口 URL 无效: %w", err)
	}
	seedURL, err := url.Parse(seedNorm)
	if err != nil {
		return nil, fmt.Errorf("入口 URL 无效: %w", err)
	}
	c.seed = seedURL

	// channel 容量：jobsCh 用于流控；resCh 需容纳取消时的全部 in-flight 结果
	// （上界 = jobsCh 缓冲 + workers），避免取消时 worker 阻塞在 resCh 导致死锁。
	jobsCap := c.opts.workers * 2
	resCap := c.opts.workers*3 + 1 // > jobsCap + workers
	jobsCh := make(chan crawlTask, jobsCap)
	resCh := make(chan crawlResult, resCap)

	report := &crawlReport{Seed: seedNorm, StartedAt: time.Now()}

	var wg sync.WaitGroup
	for i := 0; i < c.opts.workers; i++ {
		wg.Add(1)
		go c.worker(ctx, jobsCh, resCh, &wg)
	}

	// —— 主调度：独占 visited / queue / dispatched / used / pages ——
	visited := map[string]struct{}{}
	used := map[string]bool{}
	var pages []crawlResult
	stats := crawlStats{}

	visited[seedNorm] = struct{}{}
	queue := []crawlTask{{url: seedNorm, depth: 0}}
	dispatched := 1
	inFlight := 0
	consecFail := 0
	dispatchCapped := dispatched >= c.opts.maxPages // maxPages=1 时只抓种子

	// flush 非阻塞地把待办队列推入 jobsCh；jobsCh 满则留下，下轮再推。
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
	for len(queue) > 0 || inFlight > 0 {
		flush()
		select {
		case <-ctx.Done():
			aborted = true
		case r := <-resCh:
			inFlight--
			stats.Total++
			c.handleResult(&r, used, &pages)

			switch r.Status {
			case statusOK:
				stats.Success++
				consecFail = 0
			case statusSkip:
				stats.Skipped++
			default:
				stats.Failed++
				consecFail++
			}
			c.logger.Printf("[%d/%d] %s %s", stats.Total, c.opts.maxPages, r.Status, r.URL)

			// 熔断：连续失败过多，疑似 Chrome 异常，停止派发。
			if consecFail >= maxConsecutiveFailures && !dispatchCapped {
				dispatchCapped = true
				c.logger.Printf("连续 %d 次失败，停止派发新任务（等待在途任务结束）", consecFail)
			}

			// 发现新链接：仅成功页、未达深度上限、未达页数上限时入队。
			if r.Status == statusOK && r.Depth < c.opts.depth && !dispatchCapped {
				for _, link := range r.Links {
					if dispatched >= c.opts.maxPages {
						dispatchCapped = true
						break
					}
					if _, seen := visited[link]; seen {
						continue
					}
					visited[link] = struct{}{}
					queue = append(queue, crawlTask{url: link, depth: r.Depth + 1})
					dispatched++
				}
			}

			// finalURL 补登去重，防 a→b→a 重定向循环。
			if r.FinalURL != "" && r.FinalURL != r.URL {
				visited[r.FinalURL] = struct{}{}
			}
		}
		if aborted {
			break
		}
	}

	close(jobsCh)
	wg.Wait()

	report.Pages = pages
	report.Stats = stats
	report.FinishedAt = time.Now()

	if aborted {
		return report, ctx.Err()
	}

	// 种子抓取失败视为整体失败（无种子则无后续可抓）。
	for _, p := range pages {
		if p.URL == seedNorm && p.Status != statusOK {
			return report, fmt.Errorf("入口页面抓取失败: %s", p.Reason)
		}
	}
	return report, nil
}

// crawlTask 是一个待抓取任务。
type crawlTask struct {
	url   string
	depth int
}

// handleResult 在主调度线程内处理单个结果：落盘 Markdown、更新文件名占用表、
// 记录到 pages。失败时（含保存失败）改写状态与原因。
func (c *Crawler) handleResult(r *crawlResult, used map[string]bool, pages *[]crawlResult) {
	if r.Status != statusOK || r.markdown == "" {
		*pages = append(*pages, *r)
		return
	}
	rel, err := URLToFilePath(r.URL, c.opts.outDir, used)
	if err != nil {
		c.logger.Printf("路径映射失败 %s: %v", r.URL, err)
		r.Status = statusFail
		r.Reason = "path: " + err.Error()
	} else if n, werr := SavePage(c.opts.outDir, rel, r.markdown); werr != nil {
		c.logger.Printf("保存失败 %s: %v", r.URL, werr)
		r.Status = statusFail
		r.Reason = "save: " + werr.Error()
	} else {
		r.File = rel
		r.Bytes = n
	}
	r.markdown = ""
	*pages = append(*pages, *r)
}

// worker 循环消费任务：限流等待 → 抓取提取 → 回灌结果。jobsCh 关闭即退出。
func (c *Crawler) worker(ctx context.Context, jobsCh <-chan crawlTask, resCh chan<- crawlResult, wg *sync.WaitGroup) {
	defer wg.Done()
	for t := range jobsCh {
		if c.limiter != nil {
			if err := c.limiter.Wait(ctx); err != nil {
				resCh <- crawlResult{URL: t.url, Depth: t.depth, Status: statusSkip, Reason: "canceled"}
				continue
			}
		}
		resCh <- c.processOne(ctx, t)
	}
}

// processOne 抓取并处理单个页面：Fetch → 原始 DOM 提取 links+title → extractFromHTML
// 提取 Markdown。htmlStr 出作用域即 GC，不缓存，防大站点内存膨胀。
func (c *Crawler) processOne(ctx context.Context, t crawlTask) crawlResult {
	res := crawlResult{URL: t.url, Depth: t.depth}

	htmlStr, finalURL, err := c.f.Fetch(t.url, c.opts.extract)
	if err != nil {
		res.Status = statusFail
		res.Reason = clipReason(err.Error())
		return res
	}
	res.FinalURL = finalURL

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStr))
	if err != nil {
		res.Status = statusFail
		res.Reason = "parse: " + clipReason(err.Error())
		return res
	}

	// 用渲染后的「原始 DOM」发现链接（噪音清理之前，保留侧栏导航）。
	res.Links = ExtractLinks(doc, finalURL, c.seed, c.opts.allowSubdomains)
	res.Title = pageTitle(doc)

	md, err := extractFromHTML(htmlStr, finalURL, c.opts.extract)
	if err != nil {
		res.Status = statusFail
		res.Reason = "convert: " + clipReason(err.Error())
		return res
	}
	res.Status = statusOK
	res.markdown = md
	return res
}

// clipReason 把失败原因截断到索引可读长度。
func clipReason(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxReasonLen {
		return s[:maxReasonLen] + "..."
	}
	return s
}
