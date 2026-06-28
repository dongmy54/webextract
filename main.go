package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// options 聚合命令行参数。
type options struct {
	rawHTML    bool          // 输出渲染后原始 HTML（调试用）
	selector   string        // 指定正文区域 CSS 选择器
	timeout    time.Duration // 渲染等待上限
	userAgent  string        // 自定义 User-Agent
	out        string        // 输出文件路径
	includeURL bool          // 输出开头以注释标注来源 URL
	waitFor    string        // 等待该选择器出现后再提取
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "webextract: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	// 子命令分发：crawl→站点爬取，nav→导航菜单提取，否则保持单页提取模式。
	if len(args) > 0 && args[0] == "crawl" {
		return runCrawl(args[1:])
	}
	if len(args) > 0 && args[0] == "nav" {
		return runNav(args[1:])
	}

	fs := flag.NewFlagSet("webextract", flag.ContinueOnError)
	var (
		raw      = fs.Bool("raw", false, "输出渲染后的原始 HTML（调试用），而非 Markdown")
		selector = fs.String("selector", "", "CSS 选择器，指定正文区域（默认自动检测 main/article）")
		timeout  = fs.Int("timeout", 60, "等待页面渲染的最大秒数")
		ua       = fs.String("user-agent", "", "自定义 User-Agent（默认模拟桌面 Chrome）")
		out      = fs.String("o", "", "写入指定文件（默认输出到标准输出）")
		srcURL   = fs.Bool("include-source-url", false, "在输出开头以 HTML 注释标注来源 URL")
		waitFor  = fs.String("wait-for", "", "等待该 CSS 选择器出现后再提取（可选）")
	)
	fs.Usage = func() {
		w := fs.Output()
		fmt.Fprintf(w, "webextract — 将任意网页的可见内容提取为标准 Markdown\n\n")
		fmt.Fprintf(w, "用法:\n  webextract [选项] <URL>\n\n选项:\n")
		fs.PrintDefaults()
		fmt.Fprintf(w, "\n示例:\n")
		fmt.Fprintf(w, "  webextract https://example.com/article\n")
		fmt.Fprintf(w, "  webextract -o out.md https://example.com/article\n")
		fmt.Fprintf(w, "  webextract --selector 'div.main' https://example.com\n")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return fmt.Errorf("缺少 URL 参数")
	}
	urlStr := fs.Arg(0)

	cfg := options{
		rawHTML:    *raw,
		selector:   *selector,
		timeout:    time.Duration(*timeout) * time.Second,
		userAgent:  *ua,
		out:        *out,
		includeURL: *srcURL,
		waitFor:    *waitFor,
	}

	fetcher, err := NewFetcher(cfg)
	if err != nil {
		return err
	}
	defer fetcher.Close()
	result, err := extract(urlStr, cfg, fetcher)
	if err != nil {
		return err
	}

	var w io.Writer = os.Stdout
	if cfg.out != "" {
		f, err := os.Create(cfg.out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	if _, err := io.WriteString(w, result); err != nil {
		return err
	}
	if cfg.out == "" && len(result) > 0 && result[len(result)-1] != '\n' {
		io.WriteString(w, "\n")
	}
	return nil
}

// runCrawl 解析 crawl 子命令参数并驱动一次 BFS 站点爬取。
func runCrawl(args []string) error {
	fs := flag.NewFlagSet("webextract crawl", flag.ContinueOnError)
	var (
		depth        = fs.Int("depth", 2, "最大爬取深度（入口页=0）")
		maxPages     = fs.Int("max-pages", 100, "最大抓取页面数（含入口）")
		workers      = fs.Int("workers", 5, "并发抓取数量")
		rate         = fs.Float64("rate-limit", 2, "每秒最大请求数（限流，0=不限）")
		outDir       = fs.String("output", "output", "Markdown 输出目录")
		allowSub     = fs.Bool("allow-subdomains", false, "允许抓取同注册域的子域（默认仅同 host）")
		crawlTimeout = fs.Int("crawl-timeout", 1800, "爬取总超时秒数（0=不限）")
		// 继承单页提取参数，保证每个页面与单页模式提取行为一致。
		selector = fs.String("selector", "", "CSS 选择器，指定正文区域（默认自动检测 main/article）")
		timeout  = fs.Int("timeout", 60, "单页渲染等待最大秒数")
		ua       = fs.String("user-agent", "", "自定义 User-Agent（默认模拟桌面 Chrome）")
		srcURL   = fs.Bool("include-source-url", false, "在每个 Markdown 开头以注释标注来源 URL")
		waitFor  = fs.String("wait-for", "", "等待该 CSS 选择器出现后再提取（可选）")
	)
	fs.Usage = func() {
		w := fs.Output()
		fmt.Fprintf(w, "webextract crawl — 从入口 URL 出发，BFS 爬取整个站点并输出 Markdown\n\n")
		fmt.Fprintf(w, "用法:\n  webextract crawl <URL> [选项]\n\n选项:\n")
		fs.PrintDefaults()
		fmt.Fprintf(w, "\n示例:\n")
		fmt.Fprintf(w, "  webextract crawl https://docs.example.com\n")
		fmt.Fprintf(w, "  webextract crawl https://docs.example.com --depth 3 --max-pages 500 --workers 10 --rate-limit 2\n")
	}
	// 标准 flag 包遇到首个位置参数即停止解析，因此支持「URL 在前、选项在后」
	// （见技术方案示例）需先把位置参数重排到选项之后。
	if err := fs.Parse(reorderForFlagParse(args, boolFlagNames(fs))); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return fmt.Errorf("缺少 URL 参数")
	}
	urlStr := fs.Arg(0)

	switch {
	case *depth < 0:
		return fmt.Errorf("--depth 不能为负")
	case *maxPages < 1:
		return fmt.Errorf("--max-pages 至少为 1")
	case *workers < 1:
		return fmt.Errorf("--workers 至少为 1")
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}

	extractCfg := options{
		selector:   *selector,
		timeout:    time.Duration(*timeout) * time.Second,
		userAgent:  *ua,
		includeURL: *srcURL,
		waitFor:    *waitFor,
	}
	opts := crawlOptions{
		seed:            urlStr,
		depth:           *depth,
		maxPages:        *maxPages,
		workers:         *workers,
		ratePerSec:      *rate,
		outDir:          *outDir,
		allowSubdomains: *allowSub,
		extract:         extractCfg,
		crawlTimeout:    time.Duration(*crawlTimeout) * time.Second,
	}

	fetcher, err := NewFetcher(extractCfg)
	if err != nil {
		return err
	}
	defer fetcher.Close()

	ctx := context.Background()
	if opts.crawlTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.crawlTimeout)
		defer cancel()
	}

	crawler := NewCrawler(fetcher, opts)
	report, runErr := crawler.Run(ctx)

	// 即使 Run 中途出错，也尽量写出已抓取页面的索引。
	if report != nil {
		if werr := WriteIndexJSON(*outDir, report); werr != nil {
			fmt.Fprintf(os.Stderr, "webextract: 写 index.json 失败: %v\n", werr)
		}
		if werr := WriteIndexMarkdown(*outDir, report); werr != nil {
			fmt.Fprintf(os.Stderr, "webextract: 写 index.md 失败: %v\n", werr)
		}
		fmt.Fprintf(os.Stderr, "\n爬取完成：%d 页（成功 %d，失败 %d，跳过 %d），耗时 %s\n",
			report.Stats.Total, report.Stats.Success, report.Stats.Failed, report.Stats.Skipped, report.Duration())
		fmt.Fprintf(os.Stderr, "输出目录：%s\n", *outDir)
	}
	return runErr
}

// runNav 解析 nav 子命令参数，渲染首页并提取主导航菜单树，按 tree/json 输出。
func runNav(args []string) error {
	fs := flag.NewFlagSet("webextract nav", flag.ContinueOnError)
	var (
		format     = fs.String("format", "tree", "输出格式：tree（人类可读树形）/ json（供第二阶段消费）")
		maxDepth   = fs.Int("max-depth", 0, "导航菜单最大层级（1=仅一级，2=含二级，0=不限）")
		selector   = fs.String("selector", "", "显式指定导航容器 CSS 选择器，覆盖自动检测")
		all        = fs.Bool("all", false, "保留站外链接（默认仅保留与入口同注册域的链接）")
		includeURL = fs.Bool("include-url", false, "tree 输出中在每个菜单项后附 URL")
		out        = fs.String("o", "", "写入指定文件（默认输出到标准输出）")
		timeout    = fs.Int("timeout", 60, "渲染等待的最大秒数")
		ua         = fs.String("user-agent", "", "自定义 User-Agent（默认模拟桌面 Chrome）")
		waitFor    = fs.String("wait-for", "", "等待该 CSS 选择器出现后再提取（SPA 站点用）")
	)
	fs.Usage = func() {
		w := fs.Output()
		fmt.Fprintf(w, "webextract nav — 从首页提取主导航菜单树（菜单名 + URL）\n\n")
		fmt.Fprintf(w, "用法:\n  webextract nav <URL> [选项]\n\n选项:\n")
		fs.PrintDefaults()
		fmt.Fprintf(w, "\n示例:\n")
		fmt.Fprintf(w, "  webextract nav https://example.com\n")
		fmt.Fprintf(w, "  webextract nav https://example.com --format json -o nav.json\n")
	}
	if err := fs.Parse(reorderForFlagParse(args, boolFlagNames(fs))); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return fmt.Errorf("缺少 URL 参数")
	}
	urlStr := fs.Arg(0)
	if *format != "tree" && *format != "json" {
		return fmt.Errorf("--format 只支持 tree 或 json，得到 %q", *format)
	}
	if *maxDepth < 0 {
		return fmt.Errorf("--max-depth 不能为负")
	}

	cfg := options{
		timeout:   time.Duration(*timeout) * time.Second,
		userAgent: *ua,
		waitFor:   *waitFor,
	}
	fetcher, err := NewFetcher(cfg)
	if err != nil {
		return err
	}
	defer fetcher.Close()

	htmlStr, finalURL, err := fetcher.Fetch(urlStr, cfg)
	if err != nil {
		return fmt.Errorf("抓取首页失败: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStr))
	if err != nil {
		return fmt.Errorf("解析 HTML 失败: %w", err)
	}
	seedNorm, err := NormalizeURL(finalURL)
	if err != nil {
		return fmt.Errorf("入口 URL 无效: %w", err)
	}
	seedURL, err := url.Parse(seedNorm)
	if err != nil {
		return fmt.Errorf("入口 URL 无效: %w", err)
	}

	items := ExtractNav(doc, finalURL, seedURL, navOptions{
		maxDepth: *maxDepth,
		selector: *selector,
		all:      *all,
	})
	tree := NavTree{Seed: seedNorm, Items: items}

	var output string
	if *format == "json" {
		b, err := json.MarshalIndent(tree, "", "  ")
		if err != nil {
			return fmt.Errorf("序列化 JSON 失败: %w", err)
		}
		output = string(b)
	} else {
		output = RenderNavTree(tree, *includeURL)
	}

	var w io.Writer = os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	if _, err := io.WriteString(w, output); err != nil {
		return err
	}
	if len(output) > 0 && output[len(output)-1] != '\n' {
		io.WriteString(w, "\n")
	}
	return nil
}

// boolFlagNames 返回 fs 中所有布尔 flag 的名称集合。布尔 flag 不带单独的值参数，
// 重排参数时不应把其后一个 token 当作它的值。
func boolFlagNames(fs *flag.FlagSet) map[string]bool {
	names := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			names[f.Name] = true
		}
	})
	return names
}

// reorderForFlagParse 把位置参数（如 URL）移到所有选项之后，使标准 flag 包能正确
// 解析「URL 在前、选项在后」的写法。boolFlags 标记无需单独值参数的布尔选项。
// 已用 --name=value 形式给出的值不会被拆分。
func reorderForFlagParse(args []string, boolFlags map[string]bool) []string {
	var positional []string
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--": // 显式终止选项解析，其后均为位置参数
			positional = append(positional, args[i+1:]...)
			i = len(args)
		case len(a) > 1 && a[0] == '-': // 选项
			out = append(out, a)
			if strings.Contains(a, "=") {
				continue // 值已内含
			}
			name := strings.TrimLeft(a, "-")
			if !boolFlags[name] && i+1 < len(args) {
				out = append(out, args[i+1]) // 带值选项的值
				i++
			}
		default: // 位置参数
			positional = append(positional, a)
		}
	}
	return append(out, positional...)
}
