# 07 · cli / npx / 辅助工具链

## cli/ —— 终端 TUI（`@maximhq/bifrost-cli`）

独立 Go module，构建名为 `bifrost` 的终端应用：一个**编码助手多路复用器**——在带标签页的 PTY 终端里运行 Claude Code 等编码"harness"，并让它们统一通过 Bifrost 网关调用模型（从而获得统一计费、日志、路由）。

- 能力：会话恢复、git worktree 隔离会话（`--worktree`）、配置/档案管理、密钥管理、MCP 接线、harness 安装。
- 旗标与子命令：`-config`、`-no-resume`、`-worktree`；`update`（自升级）、`version`。
- 内部包：`app`、`harness`、`runtime`（PTY/回滚缓冲/SGR）、`tui`、`installer`、`config`、`secrets`、`mcp`、`apis`、`update`。

## npx/ —— 三个 npm 启动器

都是很薄的 Node 包装（`bin.js`）：从 `downloads.getmaxim.ai` 下载对应平台的预编译 Go 二进制并 exec：

| 包 | 下载并运行 | 说明 |
|---|---|---|
| `@maximhq/bifrost` | `bifrost-http` 网关 | 解析最新版本（可用 `--transport-version` 钉版本），缓存在系统缓存目录（macOS：`~/Library/Caches/bifrost/<版本>/bin`），透传 `-port`（默认 8080）、`-host`、`-app-dir`、`-log-level`、`-log-style` |
| `@maximhq/bifrost-cli` | 上述终端 TUI | 装到 `~/.bifrost/bin`，带 SHA-256 校验和 PATH 配置 |
| `@maximhq/bifrost-migration-cli` | LiteLLM 迁移工具 | 见下 |

## cmd/ 与 scripts/

- `cmd/e2eseed`：为 OSS API e2e 套件向配置/日志库写入种子数据的小工具（独立 module）。注意：**网关主二进制不在 cmd/，在 `transports/bifrost-http`**。
- `scripts/bifrost-migration-cli/`：LiteLLM → Bifrost 迁移工具（迁移模型、组织、团队、用户、虚拟 Key），即 npm 包背后的二进制。
- `scripts/realtime-test/`：realtime/WebSocket 演练客户端。

## community/

社区维护、经维护者评审的目录数据，合入 `dev` 后定期同步到线上平台。目前是 MCP Library（`community/mcp-library/servers.json`，由 `schema.json` 校验）。
