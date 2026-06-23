package main

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// countChromeProcs 统计 chromedp 启动的 Chrome 主进程数（通过 --remote-debugging
// 参数识别，普通用户的 Chrome 浏览器不会带此参数）。
func countChromeProcs() int {
	out, err := exec.Command("pgrep", "-f", "remote-debugging").Output()
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}

// TestFetchNoLeak 验证「每次抓取都新建 Chrome 进程」的便捷模式：每次调用结束后
// Chrome 进程都被回收、堆内存不持续增长。
func TestFetchNoLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过联网压测")
	}
	urls := []string{
		"https://example.com",
		"https://example.org",
		"https://example.com",
		"https://example.org",
		"https://example.com",
	}
	baseProcs := countChromeProcs()
	t.Logf("基线：chromedp Chrome 进程数 = %d", baseProcs)

	for i, u := range urls {
		_, _, err := fetchRenderedHTML(u, options{timeout: 40 * time.Second})
		if err != nil {
			t.Fatalf("第 %d 次抓取失败: %v", i+1, err)
		}
		time.Sleep(1500 * time.Millisecond) // 留时间让 Chrome 进程退出

		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		procs := countChromeProcs()
		t.Logf("第 %d 次完成: Chrome进程=%d  堆=%.1fMB  残留=%d",
			i+1, procs, float64(m.Alloc)/1e6, procs-baseProcs)

		if procs > baseProcs {
			t.Errorf("第 %d 次: Chrome 进程 %d 高于基线 %d，疑似进程泄漏", i+1, procs, baseProcs)
		}
	}
}

// TestFetcherReuse 验证「复用 Fetcher」模式：多个页面共享同一个常驻 Chrome 进程，
// 抓取过程中 Chrome 主进程数不增长、tab 关闭后堆内存稳定。
func TestFetcherReuse(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过联网压测")
	}
	urls := []string{
		"https://example.com",
		"https://example.org",
		"https://example.net",
		"https://example.com",
		"https://example.org",
	}
	f, err := NewFetcher(options{timeout: 40 * time.Second})
	if err != nil {
		t.Fatalf("NewFetcher 失败: %v", err)
	}
	defer f.Close()

	var maxHeap float64
	steadyProcs := -1
	for i, u := range urls {
		_, _, err := f.Fetch(u, options{timeout: 40 * time.Second})
		if err != nil {
			t.Fatalf("第 %d 次抓取失败: %v", i+1, err)
		}
		time.Sleep(1000 * time.Millisecond) // 留时间让已关闭 tab 的 renderer 退出

		procs := countChromeProcs()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		heapMB := float64(m.Alloc) / 1e6
		if heapMB > maxHeap {
			maxHeap = heapMB
		}
		if steadyProcs < 0 {
			steadyProcs = procs
		}
		t.Logf("第 %d 次完成: Chrome进程=%d  堆=%.1fMB", i+1, procs, heapMB)

		// 复用模式下，常驻 Chrome 进程数应稳定，不应随抓取次数增长
		// （否则说明 tab 未正确关闭、renderer 累积，即泄漏）。
		if procs > steadyProcs {
			t.Errorf("第 %d 次: Chrome 进程 %d > 首次 %d，疑似 tab 未关闭", i+1, procs, steadyProcs)
		}
	}
	if steadyProcs <= 0 {
		t.Errorf("复用模式下未见常驻 Chrome 进程（steadyProcs=%d），复用未生效", steadyProcs)
	}

	// Close 后 Chrome 进程应全部退出。
	f.Close()
	time.Sleep(1500 * time.Millisecond)
	if n := countChromeProcs(); n != 0 {
		t.Errorf("Close 后仍有 %d 个 Chrome 进程，疑似未清理", n)
	}
	t.Logf("复用模式: 5 页共享常驻 Chrome(%d 进程)，峰值堆 %.1fMB，Close 后清零", steadyProcs, maxHeap)
}
