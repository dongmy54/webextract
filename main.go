package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"
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
