# 额度模板

本模块保存可复用的额度配置模板。第 1 期只提供模板管理，不绑定员工、不绑定虚拟密钥，也不参与请求前检查或费用结算。

调用链：

- `templates.go`：模板配置、周期和模型模式校验。
- `persistence/migration.go`：创建 `ee_usage_templates` 表并登记迁移。
- `persistence/templates.go`：在身份事务内实时检查 `Usage.View` / `Usage.Manage`，执行模板 CRUD。
- `http/templates.go`：解析严格 JSON、复用会话与同源校验，转换四个 POST 接口。
- `app/identity.go`：装配迁移、存储和路由；不在这里编排模板业务。

模板保存的是配置快照。后续个人分配会复制配置，模板更新不会反向修改已分配用户；本期没有个人副本或用量表。模板配置不要求对应厂商已经存在，价格和额度执行由后续期处理。
