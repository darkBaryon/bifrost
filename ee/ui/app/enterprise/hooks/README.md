# 工作台外壳基础信息

公共外壳通过 `@enterprise/hooks/useConsoleConfig` 读取连接状态、环境标识和重启提示。EE 使用个人会话可访问的基础信息接口，完整设置继续走原有权限检查；普通版 fallback 保留原配置查询和旧向导。

| 文件 | 职责 |
|---|---|
| `useConsoleConfig.ts` | 注入独立基础查询，复用 Config 失效通知与会话认证开关，关闭 EE 旧向导。 |

对应测试依赖独立 vitest 配置，放在 [../../../tests/console/](../../../tests/console/README.md)，不随覆盖层链入 `ui/`。

首次失败不视为就绪；已有成功缓存时保留原正文。基础信息不能写入 `getCoreConfig` 缓存，也不携带完整设置或 EE 内部重启原因。关闭向导必须同时覆盖 Widget 挂载和 Sidebar 的查询、恢复入口。
