# 宿主认证接入

本包把 EE 身份服务接到宿主路由上，实现 `server.ConsoleAuthProvider`：替换管理鉴权和旧会话处理者，推理注册链及 VK 规则保持。

| 文件 | 职责 |
|---|---|
| [auth.go](auth.go) | 旧管理员凭据导入、管理路由鉴权中间件、公开入口与 MCP 临时令牌例外、WS 握手与重验回调 |
| [config.go](config.go) | `/api/config` 的认证投影与写入预检 |
| [auth_test.go](auth_test.go) | 真实路由链的身份边界、来源冲突、配置同值回送、URL 票据脱敏与临时令牌精确范围 |

身份服务、HTTP 适配、部署选项和宿主 logger 由 [app](../app/README.md) 装配后注入；本包不读环境变量、不迁移表、不用标准库 `log`。管理请求默认要求当前正常的主管理员会话，通知等上游能力通过 localAdmin 兼容标记放行；WS 每次推送前重验会话。公开协议路径与 MCP 临时令牌按固定 method/route 处理，不接受动态管理白名单。配置 GET 投影只读认证状态，PUT 同值回送剔除旧 auth_config 后继续宿主操作，实际认证变更整请求拒绝。数据库及后台 worker 生命周期仍归宿主。
