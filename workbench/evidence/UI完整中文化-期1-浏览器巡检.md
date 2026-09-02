# UI完整中文化 期1 — 浏览器人工巡检记录

> 用途:补做方案 v4「实施偏差记录」第1条留下的遗留动作(人工浏览器巡检)。该条当时以"环境无浏览器工具"为由未执行,本次核实该前提已不成立并实际执行。
>
> 执行时间:2026-08-28 | 执行方式:Playwright 1.62.1 + `channel: 'chrome'`(本机 Google Chrome 151.0.7922.174)| 被测:`localhost:3000`(vite dev)+ `localhost:8080`(bifrost-http)

## 前提变更说明

方案 v4 偏差记录第1条载明:"本环境仅配置了 notion 一个 MCP server(未授权),无浏览器工具;`npx playwright install chromium` 下载卡在 4KB"。

本次核实:
- `npx playwright --version` → `1.62.1`(已存在于 npx 缓存 `~/.npm/_npx/31e32ef8478fbf80`)
- `/Applications/Google Chrome.app` 存在
- 用 `channel: 'chrome'` 复用已装 Chrome,**绕开当初卡住的 chromium 下载环节**,启动探针成功

即当初的阻塞是"下载 chromium 失败",不等于"无法驱动浏览器"。前提已不成立,巡检可真做。

## 巡检范围与方法

覆盖需求「目标4」所列页面(侧边栏全部一级入口):仪表盘 / LLM 日志 / MCP 日志 / 连接器 / 日志设置 / 插件 / Webhook / 集群配置 / 提示词仓库 / 技能仓库,共 10 页。

判定方法:遍历 DOM 可见叶子文本节点(排除 `script`/`style`/`code`/`pre`/svg `<title>`/monaco 编辑器/日志正文),剔除含中文的节点,再按需求「非目标」剔除品牌名与技术标识(模型名、厂商名、单位如 `tok/s`、版本号等),剩余即疑似英文残留。

## 结论

**汉化主体确实生效**:侧边栏、仪表盘、筛选器、按钮等主要界面均为中文(`仪表盘`/`LLM 日志`/`连接器`/`治理`/`护栏`/`虚拟密钥`/`路由引擎` 等),动态句规则在运行时正常工作。

**但发现 scanner `--check` 报「清单为空」与页面实际英文残留不符**,且成因是 5 类**机制性提取盲区**(非偶发漏词)。这些残留在源码中真实存在、**不在白名单里**、scanner 从未报出。

## 5 类盲区(均经 `extractFromSource` 程序化复验)

用 `import { extractFromSource }` 直接喂入真实源码行,验证脚本 `/tmp/zh-audit/verify-blind.mjs`:

| 类 | 实例 | 提取结果 | 根因 |
|---|---|---|---|
| A 对象属性 `title:` | `title: "User Success Rate"`([logs/page.tsx:499](../../ui/app/workspace/logs/page.tsx#L499)) | `found=[]` | 通道② 正则仅匹配 JSX 的 `title=`,不匹配对象字面量的 `title:` |
| B 对象值映射 | `"/workspace/observability": "Observability Connectors"`([topbar.utils.ts:55](../../ui/components/topbar.utils.ts#L55)) | `found=[]` | 无任何通道覆盖「字符串键 → 字符串值」映射表 |
| C 自闭合标签后文本 | `<Plus className="size-4" /> Add Profile`([otelFormFragment.tsx:239](../../ui/app/workspace/observability/fragments/otelFormFragment.tsx#L239)) | `found=[]` | 通道① 要求 `>text<`;此处文本后无 `<`,行尾即终止 |
| D `_fallbacks/` 整目录 | `title="Unlock cluster mode to scale reliably"`([clusterView.tsx:10](../../ui/app/_fallbacks/enterprise/components/cluster/clusterView.tsx#L10)) | 提取成功,但文件从未被读 | `walkFiles` 显式 `if (name === "_fallbacks") continue`(zh-coverage.mjs:183);该目录 43 个 tsx 全部未扫,且其内容在社区版实际渲染(集群配置页) |
| E 运行时合成文本 | 页面显示 `Skills Repo` | 源码中该字符串**不存在** | `deriveTitleFromPathname` 由 URL 段 `skills-repo` split `-` 后标题化生成(topbar.utils.ts:77)。字典有 `"Skills Repository"` 却无 `"Skills Repo"`,故 `--check` 正确地不报 —— grep 类 scanner 结构上不可见此类文本 |

## 规模估计(粗下界,非精确账目)

- A 类:`grep -rnE '^\s*(title\|label\|description\|placeholder\|tooltip):\s*"[A-Z][^"]{3,}"' app components hooks lib`(排除 `_fallbacks`)→ **390 处**。按 `NR%22` 均匀抽样 18 条人工判读,**18/18 全部为真实用户可见 UI 文案**(如 `label: "Beta Headers"`、`description: "Budgets and rate limits"`、`label: "Max concurrent deliveries"`),无一条是 schema/配置键噪声。故 390 应视作基本全真,而非需大幅折扣的粗匹配上限。
- B 类:`routeTitleOverrides` 内英文值 **20 处**(topbar 面包屑标题,页面顶部常驻可见)。
- C 类:**7 处**。
- D 类:`_fallbacks/` 下 **43 个 tsx** 未扫描。
- E 类:凡 `routeTitleOverrides` 未覆盖的路由,其 topbar 标题均由 slug 合成,数量随路由表增长,无法用 grep 枚举。

## 与代码评审2 建议项的关系

代码评审2 建议 2 已预警「跨节点碎片账目不完整…启发式提取(非 AST)固有盲区」。本次巡检**证实该预警成立,且范围比其描述更广**:评审2 举的例子是"两个 `<span>` 之间的中段文本"(跨节点碎片子集),而 A/B/D/E 四类均**不是**跨节点碎片,是独立完整字符串,其中 D 类更是纯粹的目录遍历遗漏(与启发式无关,属实现缺陷)。

## 附带发现(非本案范围)

- svg `<title>MCP 客户端图标</title>` 会被 `textContent` 与相邻链接文本拼接,读作 `MCP 客户端图标MCP 日志`。屏幕上不显示,渲染正常,**虚警**,已排除。
- 控制台 2 条既有错误(与汉化无关):一个 404 资源;一条 React `forwardRef render functions accept exactly two parameters` 警告。

## 可重放命令

```bash
# 浏览器启动探针
node /tmp/zh-audit/probe1.mjs
# 10 页残留巡检
node /tmp/zh-audit/audit.mjs
# 5 类盲区程序化复验
node /tmp/zh-audit/verify-blind.mjs
# 对照:scanner 自称清零
cd ui && node scripts/zh-coverage.mjs --check   # → 覆盖率检查通过:清单为空
```

截图存于 `/tmp/zh-audit/*.png`(10 页,未纳入版本库)。
