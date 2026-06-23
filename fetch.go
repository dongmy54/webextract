package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/chromedp/chromedp"
)

// Fetcher 持有一个长生命周期的无头 Chrome 进程（通过 ExecAllocator），可复用抓取
// 多个页面，避免每次抓取都冷启动 Chrome（启动开销约 1-2 秒）。每个页面在独立的
// tab 中渲染，抓取完成即关闭该 tab；Chrome 主进程常驻，直到调用 Close 释放。
type Fetcher struct {
	allocCtx      context.Context
	allocCancel   context.CancelFunc
	browserCtx    context.Context // 常驻 browser 会话；Fetch 从它派生新 tab 以复用
	browserCancel context.CancelFunc
}

// NewFetcher 创建一个常驻无头 Chrome。在 allocator 之上建立一个 browser 会话，
// 后续 Fetch 从该会话派生新 tab，从而复用同一个常驻 Chrome 进程。browser 在第一
// 次 Fetch 时懒启动，并在 Close 时终止进程并清理临时用户数据目录。
func NewFetcher(cfg options) (*Fetcher, error) {
	allocCtx, allocCancel := newAllocatorContext(cfg)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	// 显式启动 browser 并将其绑定到 browserCtx，这样后续 Fetch 派生的 tab 才能
	// 复用同一个常驻 Chrome；否则每个 tab 会各自启动一个独立 browser。
	if err := chromedp.Run(browserCtx); err != nil {
		browserCancel()
		allocCancel()
		return nil, fmt.Errorf("启动无头 Chrome 失败: %w", err)
	}
	return &Fetcher{
		allocCtx:      allocCtx,
		allocCancel:   allocCancel,
		browserCtx:    browserCtx,
		browserCancel: browserCancel,
	}, nil
}

// Close 终止常驻 Chrome 进程并清理其临时用户数据目录。应使用 defer 调用。
func (f *Fetcher) Close() {
	// 先关闭 browser（所有 tab 随之关闭），再终止 Chrome 进程并清理临时目录。
	if f.browserCancel != nil {
		f.browserCancel()
	}
	if f.allocCancel != nil {
		f.allocCancel()
	}
}

// Fetch 在一个独立 tab 中渲染并抓取单个 URL，复用常驻 Chrome 进程。
// 抓取结束后该 tab 自动关闭（defer cancel），Chrome 主进程保持常驻。
func (f *Fetcher) Fetch(urlStr string, cfg options) (string, string, error) {
	// 从常驻 browser 会话派生一个带超时的新 tab；cancel 时仅关闭该 tab，复用的
	// Chrome 主进程不受影响。
	ctx, cancel := context.WithTimeout(f.browserCtx, cfg.timeout)
	defer cancel()
	ctx, cancel = chromedp.NewContext(ctx)
	defer cancel()
	return runFetch(ctx, urlStr, cfg)
}

// newAllocatorContext 用给定配置创建一个 ExecAllocator（启动 Chrome 的根 context）。
func newAllocatorContext(cfg options) (context.Context, context.CancelFunc) {
	return chromedp.NewExecAllocator(context.Background(), buildAllocOpts(cfg)...)
}

// buildAllocOpts 组装无头 Chrome 的启动参数。
func buildAllocOpts(cfg options) []chromedp.ExecAllocatorOption {
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts,
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("no-first-run", true),
		chromedp.WindowSize(1440, 900),
	)
	if cfg.userAgent != "" {
		opts = append(opts, chromedp.UserAgent(cfg.userAgent))
	}
	if p := findChrome(); p != "" {
		opts = append(opts, chromedp.ExecPath(p))
	}
	return opts
}

// runFetch 在给定 context（一个 tab 会话）内执行导航、等待渲染稳定并读取 HTML。
func runFetch(ctx context.Context, urlStr string, cfg options) (string, string, error) {
	actions := []chromedp.Action{
		chromedp.Navigate(urlStr),
		chromedp.WaitReady("body", chromedp.ByQuery),
	}
	if cfg.waitFor != "" {
		actions = append(actions, chromedp.WaitVisible(cfg.waitFor, chromedp.ByQuery))
	}
	actions = append(actions, waitForStableDOM(25))

	if err := chromedp.Run(ctx, actions...); err != nil {
		return "", "", fmt.Errorf("渲染页面失败: %w", err)
	}

	var finalURL, outerHTML string
	if err := chromedp.Run(ctx,
		chromedp.Location(&finalURL),
		chromedp.OuterHTML("html", &outerHTML, chromedp.ByQuery),
	); err != nil {
		return "", "", fmt.Errorf("读取页面失败: %w", err)
	}
	return outerHTML, finalURL, nil
}

// fetchRenderedHTML 是单次抓取的便捷函数：创建一个临时 Fetcher，抓取后立即关闭。
// 需要批量抓取多个页面时，应直接复用 NewFetcher 返回的 Fetcher，避免重复冷启动 Chrome。
func fetchRenderedHTML(urlStr string, cfg options) (string, string, error) {
	f, err := NewFetcher(cfg)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	return f.Fetch(urlStr, cfg)
}

// waitForStableDOM 轮询 DOM 规模指标（节点数 + 页面高度），连续多次无变化即认为
// 渲染稳定。即使页面持续变化，达到最大尝试次数后也会返回（由上层超时兜底）。
func waitForStableDOM(maxTries int) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		prev := ""
		same := 0
		for i := 0; i < maxTries; i++ {
			var cur string
			if err := chromedp.Evaluate(
				`JSON.stringify({n:document.querySelectorAll('*').length,h:(document.body&&document.body.scrollHeight)||0})`,
				&cur,
			).Do(ctx); err != nil {
				return err
			}
			if cur == prev {
				same++
				if same >= 2 {
					return nil
				}
			} else {
				same = 0
				prev = cur
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(800 * time.Millisecond):
			}
		}
		return nil
	})
}

// findChrome 在常见路径中查找 Chrome / Chromium 可执行文件，找不到返回空字符串
// （此时交由 chromedp 默认查找逻辑处理）。
func findChrome() string {
	candidates := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/snap/bin/chromium",
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}
