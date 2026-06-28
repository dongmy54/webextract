# webextract

将任意网页的**可见内容**精准转换为标准 Markdown 的命令行工具。

```bash
webextract https://code.claude.com/docs/en/overview
```

## 特性

- **动态 + 静态通吃**：基于无头 Chrome（chromedp）真实渲染页面，等待 JavaScript 执行完成后再读取 DOM，既能抓取静态 HTML，也能完整提取 SPA（React/Vue/Next.js 等）动态渲染的内容。
- **所见即所得**：自动定位正文区域（`main`/`article` 等），剥离导航、侧栏、页脚、目录、脚本样式等非正文元素，提取结果与浏览页面看到的一致。
- **完整 Markdown 语义**：正确转换标题、段落、有序/无序列表、任务列表、代码块（带语言标记）、表格、引用、链接、行内代码、删除线、水平线等。
- **框架适配**：针对文档站常见的渲染方式做了专门处理——
  - 语法高亮代码块（Shiki）：剥离高亮 `<span>`、丢弃行号、恢复多行结构与语言标记（Fern / Mintlify 等）。
  - Mintlify 的 `data-as` 语义标注：`<span data-as="p">` 等还原为真实标签，避免段落粘连。
  - 标题锚点、面包屑、页脚反馈区等噪音的清理。
- **链接原样保留**：相对链接（`/docs/quickstart`）与绝对链接均按页面原样输出，不擅自改写。

## 安装

提供三种方式，任选其一。**运行时均需本机已安装 Chrome / Chromium / Edge**（用于无头渲染，与是否安装 Go 无关）。

### 一键脚本（macOS / Linux，推荐）

无需安装 Go，一行命令即可：

```bash
curl -fsSL https://raw.githubusercontent.com/dongmy54/webextract/main/install.sh | bash
```

脚本会自动识别系统与 CPU 架构，从 [Releases](https://github.com/dongmy54/webextract/releases) 下载对应预编译二进制，安装到 `~/.local/bin`（可用 `INSTALL_DIR` 环境变量自定义，如 `INSTALL_DIR=/usr/local/bin`）。

### go install

已安装 Go（1.26+）的用户：

```bash
go install github.com/dongmy54/webextract@latest
```

### Windows

前往 [Releases](https://github.com/dongmy54/webextract/releases/latest) 下载 `webextract_<version>_windows_amd64.zip`（或 `_arm64`），解压取出 `webextract.exe` 放入 PATH 即可。

预编译二进制覆盖：`linux/amd64`、`linux/arm64`、`darwin/amd64`（macOS Intel）、`darwin/arm64`（macOS Apple Silicon）、`windows/amd64`、`windows/arm64`。

## 源码构建

需要本机已安装 [Go](https://go.dev)（版本需满足 `go.mod`，当前为 1.26+）。运行时另需 Chrome / Chromium / Edge。

```bash
go build -o webextract .
```

## 用法

```text
webextract — 将任意网页的可见内容提取为标准 Markdown

用法:
  webextract [选项] <URL>

选项:
  -include-source-url
        在输出开头以 HTML 注释标注来源 URL
  -o string
        写入指定文件（默认输出到标准输出）
  -raw
        输出渲染后的原始 HTML（调试用），而非 Markdown
  -selector string
        CSS 选择器，指定正文区域（默认自动检测 main/article）
  -timeout int
        等待页面渲染的最大秒数 (默认 60)
  -user-agent string
        自定义 User-Agent（默认模拟桌面 Chrome）
  -wait-for string
        等待该 CSS 选择器出现后再提取（可选）
```

### 示例

```bash
# 输出到终端
webextract https://openrouter.ai/docs/guides/overview/models

# 保存到文件
webextract -o page.md https://example.com/article

# 指定正文选择器
webextract --selector 'div.article-body' https://example.com

# 调试：查看渲染后的原始 HTML
webextract -raw https://example.com > debug.html
```

## 网站爬取（crawl）

从入口 URL 出发，广度优先（BFS）递归抓取整个站点，复用同一套正文提取能力，按 URL 路径输出 Markdown 文件，并生成索引。

```bash
webextract crawl https://docs.example.com \
  --depth 3 \
  --max-pages 500 \
  --workers 10 \
  --rate-limit 2
```

参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--depth` | 2 | 最大爬取深度（入口页=0，抓取深度 0..depth） |
| `--max-pages` | 100 | 最大抓取页面数（含入口） |
| `--workers` | 5 | 并发抓取数量（= 最大并发 tab 数） |
| `--rate-limit` | 2 | 每秒最大请求数（限流，0=不限；支持小数如 0.5） |
| `--output` | output | Markdown 输出目录 |
| `--allow-subdomains` | false | 允许抓取同注册域的子域（默认仅同 host） |
| `--crawl-timeout` | 1800 | 爬取总超时秒数（0=不限） |

单页提取的相关参数（`--selector` / `--timeout` / `--user-agent` / `--include-source-url` / `--wait-for`）同样适用于 crawl，作用于抓取到的每个页面。

输出结构示例：

```text
output/
├── index.json        # 机器可读索引（每页 url/标题/深度/文件/状态）
├── index.md          # 人类可读索引（按深度分组的标题→路径列表）
├── index.md ...      # 与原 URL 路径对应的 Markdown 文件
├── install.md
├── config/
│   └── mysql.md
└── api/
    └── user.md
```

特性与限制：

- **BFS + 去重**：所有链接进入队列前先规范化（去 `#fragment`、去 utm 等 tracking 参数、query 排序、host 小写、去默认端口、路径规整），避免循环引用与重复抓取。
- **站内限制**：默认仅抓取与入口同 host 的页面；`--allow-subdomains` 放宽到同注册域（基于 publicsuffix 精确判断）。
- **Worker Pool + 限流**：可配置并发与全局请求频率，降低对目标站点的压力。
- **容错**：单页失败（404/403/500/超时/解析失败）记录后继续，不影响整体；连续失败会熔断。入口页本身抓取失败则整体失败。
- **本地可跳转**：抓取完成后自动把站内链接改写为相对路径，使输出目录下的 Markdown 文件之间可点击跳转（指向未抓取页面的链接保留原样，避免产生死链）。
- **默认不读取 robots.txt**：本工具为用户主动指定目标的 CLI，请合理设置 `--rate-limit` 与 User-Agent。
- **链接来源**：从无头 Chrome 渲染后的 `<a href>` 提取（SPA 客户端路由链接可抓取）；`onclick`/`<button>` 触发的跳转无法抓取。

## 导航菜单提取（nav）

渲染首页并提取**主导航菜单树**（菜单名 + 链接 URL），输出人类可读树形或机器可读 JSON。它是「导航驱动爬取」的第一阶段：先看清站点导航结构，其 JSON 输出可作为后续按分类抓取的输入。

```bash
# 树形输出
webextract nav https://go.dev --max-depth 2

# JSON 输出（写入文件，供后续消费）
webextract nav https://go.dev --format json -o nav.json
```

树形输出示例：

```text
网站导航 — https://go.dev/

├─ Why Go
│  ├─ Case Studies
│  ├─ Use Cases
│  └─ Security
├─ Learn
├─ Docs
│  ├─ Go Spec
│  └─ Effective Go
└─ Community
```

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--format` | `tree` | 输出格式：`tree`（树形）/ `json`（供第二阶段消费） |
| `--max-depth` | `0` | 菜单最大层级（1=仅一级，2=含二级，0=不限） |
| `--selector` | `""` | 显式指定导航容器 CSS 选择器，覆盖自动检测 |
| `--all` | `false` | 保留站外链接（默认仅保留与入口同注册域的链接） |
| `--include-url` | `false` | tree 输出中在每个菜单项后附 URL |
| `--o` | `""` | 写入文件（默认标准输出） |

单页提取的 `--timeout` / `--user-agent` / `--wait-for` 同样适用。

提取原理：

- **容器定位（打分择优）**：收集页面所有候选导航容器（`nav`/`[role=navigation]`/`aside`/常见 `.nav`/`.sidebar`/`.menu` 类与 id），按「链接数 + 嵌套层级」打分取最高者——文档站「链接最多、结构最深」的左侧侧边栏因此能胜出，而不会被顶栏少量链接的导航抢先。排除 footer 内导航与「包住其它候选的外层大容器」；兜底为 header 内含 `<a>` 的块。可用 `--selector` 手动指定。
- **多分区 + ul/li 递归**：容器内若有多个并列的顶层 `<ul>` 分区（文档侧边栏常按 `<h3>` 标题分组），逐一处理并以标题作为分组父节点；对每个 `<li>` 克隆删子列表后取首个 `<a>`（无 `<a>` 时取 `<details><summary>` 等纯文本）作为本层菜单名，再递归子列表；非 ul/ol 结构回退为容器内 `<a>` 的扁平列表。
- **规范化与过滤**：复用 `NormalizeURL` 去重，默认仅保留同注册域链接（`--all` 关闭），剔除资源后缀与 `javascript:`/`mailto:` 等特殊协议，并清理图标字体连字文本。

## 工作原理

```
URL
 │
 ▼
chromedp 无头 Chrome 渲染（导航 → 等待 body → DOM 稳定轮询）
 │
 ▼
goquery 定位正文区域（main/article，取文本量最大者）
 │
 ▼
DOM 规范化（移除噪音 → 还原 data-as 语义 → 清理锚点 → 规范化代码块）
 │
 ▼
html-to-markdown 转换为 GitHub Flavored Markdown
 │
 ▼
输出清理（围栏空行 / 表格管道转义 / 连字符还原 / 去页眉页脚 / 压缩空行）
 │
 ▼
Markdown
```

## 项目结构

| 文件 | 职责 |
| --- | --- |
| `main.go` | 命令行入口、参数解析、`crawl` / `nav` 子命令分发 |
| `fetch.go` | chromedp 无头渲染抓取、Chrome 路径查找、DOM 稳定等待 |
| `pipeline.go` | 核心流程编排：抓取 → 定位 → 规范化 → 转换 → 清理（`extractFromHTML` 供 crawl 复用） |
| `extract.go` | 正文区域定位、噪音移除、`data-as` 语义还原、代码块规范化 |
| `convert.go` | HTML → Markdown 转换与输出清理 |
| `urlutil.go` | URL 规范化（去重权威）、tracking 参数剥离、站内/子域范围判定 |
| `links.go` | 从渲染后原始 DOM 提取站内链接（处理 `<base href>`、过滤资源/特殊协议） |
| `nav.go` | 从首页 DOM 提取主导航菜单树（容器定位、ul/li 递归、规范化/去重/过滤、树形渲染） |
| `crawler.go` | BFS 调度 + Worker Pool + 限流 + 去重 + 深度/页数控制 + 熔断 |
| `output.go` | URL→文件路径映射、Markdown 落盘、索引（`index.json` / `index.md`）生成 |

## 依赖

- [`github.com/chromedp/chromedp`](https://github.com/chromedp/chromedp) — Chrome DevTools Protocol 客户端，驱动无头浏览器渲染
- [`github.com/PuerkitoBio/goquery`](https://github.com/PuerkitoBio/goquery) — HTML 解析与 DOM 操作
- [`github.com/JohannesKaufmann/html-to-markdown`](https://github.com/JohannesKaufmann/html-to-markdown) — HTML 转 Markdown（启用 GitHub Flavored 插件）
- [`golang.org/x/time/rate`](https://pkg.go.dev/golang.org/x/time/rate) — 令牌桶限流（crawl 子命令）
- [`golang.org/x/net/publicsuffix`](https://pkg.go.dev/golang.org/x/net/publicsuffix) — 注册域判定（`--allow-subdomains`）
- **运行时**：Chrome / Chromium / Edge（无头渲染，随系统安装，非 Go 模块）

## 关于测试参考文件

`test-check/` 下提供了两个页面的参考 Markdown。本工具的输出与参考在**正文内容上完全一致**（关键词覆盖 100%），但存在少量结构性差异，原因在于参考文件来自页面的 **MDX 源**，而本工具从**浏览器渲染后的 HTML** 提取：

- 参考文件保留了 `<Tabs>` / `<Accordion>` / `<Info>` 等 MDX 组件的原始标签；本工具将其渲染后的内容（标签页、折叠面板）以普通 Markdown 列表/段落呈现，**内容完整但形式不同**。
- 参考文件以正文段落开头（无页面 H1）；本工具保留页面可见的 H1 标题与副标题。
- 参考文件含视觉隐藏的 `llms.txt` 索引提示框（`sr-only`）；本工具按「与人眼所见一致」的原则不提取视觉不可见内容。

这些差异不影响内容的完整性与可用性。
