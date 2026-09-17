# 账号认证

## 解决什么问题

管理控制台账号、密码和登录会话：登录后每次核实账号状态和会话版本，账号停用或改密后旧会话失效。
账号操作的权限和初始化、恢复时的附加检查由调用方注入；没有注入时，管理操作仍限系统初始化时指定的主管理员。
应用已装配角色权限策略：账号操作分别检查 Users.View 或 Users.Manage；申请通知 WebSocket 票据需要 Notifications.View，且不能处于强制改密状态。

## 代码在哪

| 文件 | 职责 |
|---|---|
| [model.go](model.go) | 账号、会话、错误和存储接口 |
| [core.go](core.go) | 服务构造、密码计算、会话验证和分页辅助 |
| [access.go](access.go) | 账号动作授权、初始化/恢复检查、可共享的账号查询能力 |
| [session.go](session.go) | 登录、登出、会话验证和一次性票据；成功登录时记录时间 |
| [accounts.go](accounts.go) | 初始化、旧账号导入、创建、启停和删除账号 |
| [passwords.go](passwords.go) | 本人改密、管理者重置、离线恢复和密码事件 |
| [persistence/store.go](persistence/store.go) | 数据读写、事务、登录限流和分页 |
| [persistence/scope.go](persistence/scope.go) | 向角色模块提供同一事务、写锁和账号查询 |
| [persistence/rows.go](persistence/rows.go)、[migration.go](persistence/migration.go) | 身份表结构与迁移 |
| [http/handler.go](http/handler.go) | HTTP入口；请求和响应转换在同目录 |
| [hasher/bcrypt.go](hasher/bcrypt.go) | 密码哈希计算 |

## 改之前要知道什么

- 删除账号需要 Users.Manage；不能删除自己、固定恢复账号或最后一个启用的主管理员。角色解绑、会话及票据清理与账号删除共用事务，密码事件保留。
- 最近登录时间仅在成功登录提交时更新；从未登录或升级前的未知历史值为空，失败登录不更新。
- 密码或状态变化要更新账号版本并撤销会话；不能只改一个字段。
- 所有身份写入共用state锁；注入的角色检查、初始化和恢复操作必须加入当前事务，失败一起回滚。
- 跨模块读取只提供账号安全信息，不暴露密码哈希、令牌或数据库对象。
- 根包只依赖标准库；数据库、HTTP和密码算法放在适配包中。连接由应用持有和关闭。
