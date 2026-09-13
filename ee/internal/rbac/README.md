# 角色与权限规则（验收批次 1A）

当前仅包含独立业务规则，依赖标准库和注入的 `Repository`。尚未连接真实数据库、identity、HTTP 或宿主管理接口；应用运行行为仍为认证基线。完整权限后端保留在 `feat/user-permissions-backend`，不能把那里的测试结论当作本批验收结果。

## 阅读顺序

| 顺序 | 文件 | 行数 | 关注点 |
|---|---|---:|---|
| 1 | [permissions.go](permissions.go) | 175 | 29 项目录、预置角色、chief 权限动态派生 |
| 2 | [model.go](model.go) | 184 | 错误、业务对象、仓储快照与事务契约 |
| 3 | [authorize.go](authorize.go) | 143 | 身份重验、权限并集与来源、每次调用重新查询 |
| 4 | [roles.go](roles.go) | 204 | 角色增删改查、输入校验、分页与预置保护 |
| 5 | [assignments.go](assignments.go) | 123 | 分配、禁止自改、最后管理员与恢复绑定规则 |

`Catalogue.Available` 沿用最终产品目录对合法入口的定义，当前不表示这些宿主功能已接入 RBAC。通知受众检查留到通知接入阶段，未带入本批。

## 验证

在 `ee/` 执行 `GOWORK=off go test -race -v ./internal/rbac`。

- [roles_test.go](roles_test.go)：目录和歧义游标输入。
- [service_test.go](service_test.go)：角色操作、并集/来源、撤权、管理员保护、失败拒绝及分页。
- [fixture_test.go](fixture_test.go)：内存仓储，仅为业务规则提供输入，不模拟 SQL 事务、真实会话或锁。

实际数据库隔离、并发保护、会话校验与 HTTP 越权需在后续批次验证。本批不会注册接口、迁移表或开放普通账号的管理权限。

分期及验收入口：[开发蓝图](../../../workbench/cases/角色权限后端/开发蓝图.md)、[批次 1A 阅读与验收](../../../workbench/evidence/角色权限后端-期1-批次1A阅读与验收.md)。
