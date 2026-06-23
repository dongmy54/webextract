package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/chromedp/chromedp"
)

// fetchRenderedHTML 用无头 Chrome 渲染 URL，返回渲染后的完整 HTML 与最终 URL。
// 静态页面可直接取到内容；SPA 等动态页面通过等待 DOM 稳定后再读取，从而拿到
// JavaScript 执行后的结果。
func fetchRenderedHTML(urlStr string, cfg options) (string, string, error) {
	allocOpts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	allocOpts = append(allocOpts,
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
		allocOpts = append(allocOpts, chromedp.UserAgent(cfg.userAgent))
	}
	if p := findChrome(); p != "" {
		allocOpts = append(allocOpts, chromedp.ExecPath(p))
	}

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer cancel()

	ctx, cancel := context.WithTimeout(allocCtx, cfg.timeout)
	defer cancel()

	ctx, cancel = chromedp.NewContext(ctx)
	defer cancel()

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
