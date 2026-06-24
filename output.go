package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// output 负责「URL → 本地文件路径」映射、Markdown 落盘与索引生成。
//
// 路径映射示例（与方案第 12 节一致）：
//
//	/                     → index.md
//	/install              → install.md
//	/config/mysql         → config/mysql.md
//	/p?id=1               → p_<hash>.md   （同 path 不同 query 用 query 的短 hash 区分）
//
// 冲突（不同 URL 映射到同一相对路径，主要由大小写不敏感/编码差异引起）由调用方
// 传入的 used 占用表消解，追加 _2、_3 后缀。该表由爬虫主调度 goroutine 独占持有，
// 因此映射过程无需加锁。

const (
	mdExt        = ".md"
	maxSegBytes  = 200 // 单个路径段最大字节数，超出则「前缀 + hash」截断
	truncKeep    = 100
)

// URLToFilePath 把 URL 映射为相对 baseDir 的 Markdown 文件路径（如 "config/mysql.md"）。
// used 记录已分配的相对路径以消解冲突，会被原地修改。
func URLToFilePath(rawURL, baseDir string, used map[string]bool) (string, error) {
	normalized, err := NormalizeURL(rawURL)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return "", err
	}

	trimmed := strings.Trim(u.Path, "/")
	var segs []string
	var base string
	if trimmed == "" {
		base = "index"
	} else {
		parts := strings.Split(trimmed, "/")
		for i := range parts {
			parts[i] = sanitizeSegment(parts[i])
		}
		base = parts[len(parts)-1]
		if len(parts) > 1 {
			segs = parts[:len(parts)-1]
		}
	}
	if base == "" {
		base = "index"
	}

	fileName := base + mdExt
	if u.RawQuery != "" {
		fileName = base + "_" + shortHash(u.RawQuery) + mdExt
	}

	parts := append(segs, fileName)
	rel := strings.Join(parts, "/")
	return resolveCollision(rel, used), nil
}

// sanitizeSegment 把单个路径段清理为合法、安全的文件名片段：
// 替换文件系统非法字符（含路径分隔符）与控制字符，避免 "."/".."，超长则截断。
func sanitizeSegment(seg string) string {
	var b strings.Builder
	b.Grow(len(seg))
	for _, r := range seg {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			b.WriteByte('_')
		default:
			if r < 0x20 || r == 0x7f {
				b.WriteByte('_')
			} else {
				b.WriteRune(r)
			}
		}
	}
	s := strings.TrimSpace(b.String())
	if s == "" || s == "." || s == ".." {
		return "_"
	}
	if len(s) > maxSegBytes {
		s = s[:truncKeep] + "_" + shortHash(s)
	}
	return s
}

// resolveCollision 在占用表 used 中登记 rel；若已被占用则追加数字后缀直到可用。
func resolveCollision(rel string, used map[string]bool) string {
	if !used[rel] {
		used[rel] = true
		return rel
	}
	dir := ""
	file := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		dir = rel[:i+1]
		file = rel[i+1:]
	}
	name := strings.TrimSuffix(file, mdExt)
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s%s_%d%s", dir, name, i, mdExt)
		if !used[cand] {
			used[cand] = true
			return cand
		}
	}
}

// shortHash 返回字符串的 8 位十六进制 FNV 哈希，用于区分同 path 不同 query 的页面。
func shortHash(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%08x", h.Sum32())
}

// SavePage 把 Markdown 内容写入 baseDir/relPath，自动创建父目录，返回写入字节数。
func SavePage(baseDir, relPath, content string) (int, error) {
	full := filepath.Join(baseDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return 0, err
	}
	data := []byte(content)
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return 0, err
	}
	return len(data), nil
}

// writeAtomic 先写临时文件再原子改名，避免崩溃留下半个索引文件。
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // rename 成功后文件已不在 tmpPath，Remove 为空操作
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// WriteIndexJSON 把爬取报告序列化为 JSON 索引（含每页 url/finalURL/title/depth/
// file/status/bytes，失败页同样记录），写入 outDir/index.json。
func WriteIndexJSON(outDir string, report *crawlReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomic(filepath.Join(outDir, "index.json"), data)
}

// WriteIndexMarkdown 生成人类可读的索引：按深度分组列出「标题 → 相对路径」。
// 仅收录抓取成功的页面，写入 outDir/index.md。
func WriteIndexMarkdown(outDir string, report *crawlReport) error {
	var b strings.Builder
	b.WriteString("# 爬取索引\n\n")
	fmt.Fprintf(&b, "入口：%s\n\n", report.Seed)
	fmt.Fprintf(&b, "- 抓取页面：%d\n- 成功：%d\n- 失败：%d\n- 耗时：%s\n\n",
		report.Stats.Total, report.Stats.Success, report.Stats.Failed, report.Duration())

	// 按深度分组；同深度内按标题排序，便于阅读。
	ok := make([]crawlResult, 0, len(report.Pages))
	for _, p := range report.Pages {
		if p.Status == statusOK && p.File != "" {
			ok = append(ok, p)
		}
	}
	sort.SliceStable(ok, func(i, j int) bool {
		if ok[i].Depth != ok[j].Depth {
			return ok[i].Depth < ok[j].Depth
		}
		return ok[i].Title < ok[j].Title
	})

	curDepth := -1
	for _, p := range ok {
		if p.Depth != curDepth {
			curDepth = p.Depth
			fmt.Fprintf(&b, "\n## 深度 %d\n\n", curDepth)
		}
		title := p.Title
		if title == "" {
			title = p.URL
		}
		// 相对路径：index.md 与其他 .md 同在 outDir 根，无需前缀。
		fmt.Fprintf(&b, "- [%s](%s)\n", title, p.File)
	}
	return writeAtomic(filepath.Join(outDir, "index.md"), []byte(b.String()))
}
