package main

import (
	"strings"
	"testing"
)

func TestRewriteLocalLinks(t *testing.T) {
	seed := mustParseURL(t, "https://code.claude.com")
	urlToFile := map[string]string{
		"https://code.claude.com/docs/zh-CN/quickstart": "docs/zh-CN/quickstart.md",
		"https://code.claude.com/docs/zh-CN/overview":   "docs/zh-CN/overview.md",
		"https://code.claude.com/docs/llms.txt":         "docs/llms.txt.md",
	}

	content := "# Overview\n" +
		"\n" +
		"参见 [快速入门](/docs/zh-CN/quickstart) 与 [llms](/docs/llms.txt)。\n" +
		"\n" +
		"外部 [GitHub](https://github.com/x) 不变。\n" +
		"\n" +
		"锚点 [返回顶部](#top) 不变。\n" +
		"\n" +
		"未抓取 [未知](/docs/zh-CN/missing) 不变。\n" +
		"\n" +
		"带锚点 [section](/docs/zh-CN/quickstart#install) 改写并保留锚点。\n" +
		"\n" +
		"```\n" +
		"代码块内 [不改写](/docs/zh-CN/quickstart)\n" +
		"```\n"

	got := RewriteLocalLinks(content, "https://code.claude.com/docs/zh-CN/overview",
		"docs/zh-CN/overview.md", urlToFile, seed, false)

	checks := map[string]string{
		"站内同级改写":  "[快速入门](quickstart.md)",
		"站内跨目录改写": "[llms](../llms.txt.md)",
		"保留锚点改写":  "[section](quickstart.md#install)",
	}
	for name, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("%s：期望包含 %q，得到:\n%s", name, want, got)
		}
	}

	noChange := []string{
		"[GitHub](https://github.com/x)",
		"[返回顶部](#top)",
		"[未知](/docs/zh-CN/missing)",
		"[不改写](/docs/zh-CN/quickstart)",
	}
	for _, want := range noChange {
		if !strings.Contains(got, want) {
			t.Errorf("期望保留 %q，得到:\n%s", want, got)
		}
	}
}

func TestRewriteLocalLinksRootFile(t *testing.T) {
	seed := mustParseURL(t, "https://code.claude.com")
	urlToFile := map[string]string{
		"https://code.claude.com/docs/zh-CN/overview": "docs/zh-CN/overview.md",
	}
	// 根目录 index.md 指向子目录文件，应得到 docs/zh-CN/overview.md。
	content := "[概述](/docs/zh-CN/overview)"
	got := RewriteLocalLinks(content, "https://code.claude.com/", "index.md", urlToFile, seed, false)
	if !strings.Contains(got, "[概述](docs/zh-CN/overview.md)") {
		t.Errorf("根文件改写错误，得到: %s", got)
	}
}
