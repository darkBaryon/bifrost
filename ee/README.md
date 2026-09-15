# EE 自有应用

[← 返回项目入口](../README.md)

EE 拥有独立 Go 模块和可执行入口，在同一进程中内嵌完整的 Bifrost HTTP 应用，复用网关能力并承载自有业务。这里的 EE 指本项目实现，上游商业版介绍不代表已实现功能。

## 代码结构

现有后端已按 [EE 后端架构](../product/架构/EE后端架构.md) 迁移：按业务能力分模块，模块内部保留职责分层。

| 路径 | 职责 |
|---|---|
| [cmd/bifrost-http/](cmd/bifrost-http/README.md) | 程序入口与 UI embed |
| [internal/app/](internal/app/README.md) | 配置、依赖装配与启动接入 |
| [internal/branding/](internal/branding/README.md) | 品牌业务；http/ 与 persistence/ 分别适配入口和存储 |
| [internal/identity/](internal/identity/README.md) | 本地账号、会话、密码事件；http/、persistence/、hasher/ 分别适配入口、存储和 bcrypt |
| [internal/rbac/](internal/rbac/README.md) | 角色、权限目录、账号角色分配及对应HTTP接口 |
| [internal/host/](internal/host/README.md) | 把 EE 认证接到上游：管理路由鉴权、会话路由、WebSocket 重验、配置投影 |
| `ui/` | 自有前端，当前通过覆盖层与根目录 UI 共同构建 |
| [Makefile](Makefile)、`scripts/` | 开发、构建与验证 |

双方共享 HTTP Server、Router 和已有数据库连接；EE 内嵌上游 Handler 链，按业务需要增加外层处理。账号认证和角色管理接口已接入；厂商、虚拟密钥、治理、路由、MCP和提示词管理已接入角色权限；日志、插件、设置、通知及认证页面仍在后续范围。

前端继续放在 `ee/ui/`，与 Go 后端保持各自的依赖和构建职责；本次迁移保留现有覆盖方式。

## 开发与构建

在仓库根执行：

```bash
make -C ee help
make -C ee dev     # EE API，默认 8080；前端开发服务器另行启动
make -C ee build   # 构建并嵌入 UI，输出 ee/tmp/bifrost-http
make -C ee test    # EE 模块的 go vet + go test
make -C ee smoke            # 构建并执行隔离冒烟：品牌 + SQLite 身份
make -C ee smoke-identity   # 双节点 PostgreSQL 身份冒烟，需要 IDENTITY_TEST_POSTGRES_DSN
```

首次前端构建先在根 `ui/` 执行 `npm ci`。workspace 目标将 EE 加入根 go.work（缺失时初始化），dev/build 同步前端覆盖层。构建产物位于 `cmd/bifrost-http/ui/`，不入库。

冒烟使用临时配置、随机端口和自有进程：品牌冒烟验证宿主接入、品牌操作与重启保留，身份冒烟验证初始化竞争、账号与密码流程、跨节点撤销、WS 与离线恢复；两者共用 `scripts/identity-smoke.py` 里的进程夹具。骨架探针接口、空插件、表注册和页面/响应标记已移除；已有数据库中的旧测试表不再使用。

## 账号认证

参见[接口](docs/账号认证接口.md)和[升级与恢复](docs/账号认证升级与恢复.md)。EE管理默认强制认证，旧管理员重新登录，新实例需要初始化密钥；管理脚本迁移为Cookie及同源Origin。

## 文档

[产品与技术决策](../product/README.md) · [开发工作台](../workbench/README.md) · [编码规范](../workbench/规范/项目/编码规范.md)
