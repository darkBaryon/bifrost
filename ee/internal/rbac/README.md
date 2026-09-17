# 角色与权限

## 解决什么问题

账号登录只能证明“你是谁”，不能决定“你能做什么”。本包把控制台操作定义为固定权限，
让管理员将权限组成角色、把角色分配给账号，并判断账号能否执行某项操作、权限来自哪些角色。
同时保护预置角色和最后一个可用的主管理员，避免误操作后无人能管理系统。

本包包含业务规则、数据库实现和身份协作。应用启动时装配本包，提供角色、权限目录和账号角色接口。已有管理接口通过[host适配层](host/README.md)接入角色授权；日志、Webhook、插件、设置、通知和长连接均使用当前账号的角色权限。
账号、密码和会话由身份模块负责；调用模型使用的虚拟密钥权限也不属于这里。

## 代码在哪

角色的数据定义和操作放在一起；账号身份、权限结果和权限检查放在一起。按下面的顺序阅读即可。

| 文件 | 职责 |
|---|---|
| [permissions.go](permissions.go) | 模块与权限目录、权限标识查询 |
| [roles.go](roles.go) | 角色的数据结构、三个预置角色和权限处理，以及增删改查、分页和删除保护 |
| [authorize.go](authorize.go) | 操作者的身份信息、权限结果，以及身份和权限检查 |
| [assignments.go](assignments.go) | 给账号分配角色，保护最后主管理员，以及初始化和恢复时使用的账号信息与角色绑定 |
| [repository.go](repository.go) | `Queries` 提供查询，`Tx` 增加修改，`Repository` 管理事务；数据库实现需遵守的约定 |
| [errors.go](errors.go) | 对外返回的错误，以及数据库内部错误的隐藏处理 |
| [persistence/rows.go](persistence/rows.go) | 角色、角色权限和账号角色三张表 |
| [persistence/store.go](persistence/store.go) | 实现查询/事务接口，复用身份模块提供的连接和账号查询 |
| [persistence/roles.go](persistence/roles.go) | 角色及分配关系的数据库读写 |
| [persistence/migration.go](persistence/migration.go) | 建表、预置角色及首次绑定，重复执行不覆盖用户修改 |
| [identity/policy.go](identity/policy.go) | 把账号动作对应到权限检查，将初始化和恢复绑定加入身份事务 |
| [http/handler.go](http/handler.go) | 九个接口的路由注册，以及共同的来源检查、Cookie认证和响应输出；错误方法由宿主 RootGuard 统一处理 |
| [http/api.go](http/api.go) | 九个接口按角色管理、账号角色分配和权限查询分组，各组输入和返回字段就近放置 |
| [http/protocol.go](http/protocol.go) | 公共JSON校验、编号解析，以及HTTP错误状态和响应内容 |
| [http/matrix.go](http/matrix.go) | 将权限转换为前端使用的功能开关 |
| [host/](host/README.md) | 为Bifrost已有接口检查角色权限、保护敏感输入和返回字段 |
| [roles_test.go](roles_test.go) | 权限目录与分页游标的规则测试 |
| [service_test.go](service_test.go) | 从业务入口验证角色操作、分配、撤权和保护规则 |
| [fixture_test.go](fixture_test.go) | 为规则测试提供内存数据，不模拟真实会话或数据库事务 |

## 改之前要知道什么

- `Security.ChangeCredentialDestination` 允许将已有凭据用于新地址或连接信任设置；默认仅主管理员拥有，可显式授予其他角色，使用时仍需对应模块管理权限。
- 多角色权限取并集；Manage 不包含 View 或敏感权限。目录中的可用性标记不代替接口授权。
- 主管理员身份按稳定标识判断，改名字不会改变身份；它始终拥有全部目录权限，其他预置角色可改权限但不可删除。
- 每次操作都要重新检查身份和权限，授权结果不能跨请求缓存；调用方传来的账号标识不能当作认证凭据。
- 不能修改自己的角色分配；移除角色或停用账号时，至少保留一个启用的主管理员。启用但需改密的账号仍计入保护。
- 判权、保护检查和写入必须处于同一事务，并与身份模块共用写锁；真实存储接入时必须保证失败整体回滚。
- 删除角色要计入停用账号的关联；离线恢复只补回恢复账号的主管理员角色，保留其他角色。

规则测试：在 `ee/` 执行 `GOWORK=off go test -race ./internal/rbac`。
存储集成测试在 `persistence/*_test.go`，默认使用临时SQLite；设置 `RBAC_TEST_POSTGRES_DSN` 后使用独立PostgreSQL schema。
九个自有接口的输入、返回值和权限见[角色权限接口](../../docs/角色权限接口.md)。
接口及管理功能的接入边界见[开发蓝图](../../../workbench/cases/角色权限后端/开发蓝图.md)。
