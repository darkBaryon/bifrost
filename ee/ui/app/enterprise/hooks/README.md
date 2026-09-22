# 工作台外壳基础信息

公共外壳通过 `@enterprise/hooks/useConsoleConfig` 读取连接状态、环境标识和重启提示。EE 使用个人会话可访问的基础信息接口，完整设置继续走原有权限检查；普通版 fallback 保留原配置查询和旧向导。

| 文件 | 职责 |
|---|---|
| `useConsoleConfig.ts` | 注入独立基础查询，复用 Config 失效通知与会话认证开关，关闭 EE 旧向导。 |
| `useConsoleConfig.test.ts` | 验证真实 RTK 请求、凭据、缓存隔离、设置失效刷新、失败恢复、skip 与普通版兼容。 |
| `consoleTestHarness.tsx` | 仅供测试使用的真实 store、Fetch 响应与 hook 缓存渲染支持。 |
| `consoleSidebar.test.tsx` | 验证环境、重启提示与旧向导查询/恢复入口开关。 |
| `consoleTopbar.test.tsx` | 验证退出入口与原 mutation/登录跳转，包括服务端退出失败。 |
| `consoleShell.test.tsx` | 验证外壳首次失败/已有缓存刷新失败、连接状态、重试及公共和临时页面的跳过条件。 |
| `../../../console-bootstrap.vitest.config.ts` | 独立测试配置，明确 EE hook 与其余 fallback 的别名以及符号链接解析。 |

从隔离源副本的 `ui/` 运行 `node node_modules/vitest/vitest.mjs run --config ../ee/ui/console-bootstrap.vitest.config.ts`。测试不添加依赖，也不执行 `copy-build`；`ui/node_modules` 与 `ee/ui/node_modules` 可链接到同一份已有安装，后者供独立配置与 EE 测试解析包依赖。Node 渲染测试核查 skip 状态和外壳传参，不替代浏览器对“零请求”的实测。

首次失败不视为就绪；已有成功缓存时保留原正文。基础信息不能写入 `getCoreConfig` 缓存，也不携带完整设置或 EE 内部重启原因。关闭向导必须同时覆盖 Widget 挂载和 Sidebar 的查询、恢复入口。
