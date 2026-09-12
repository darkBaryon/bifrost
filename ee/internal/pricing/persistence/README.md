# 国内定价存储适配

把 pricing 定义的覆盖 CRUD、厂商列表和目录增量接口适配到 ConfigStore 与 ModelCatalog。只复用上游完整操作，不接触 HTTP Server、不建新表，连接生命周期属于宿主。

| 文件 | 职责 |
|---|---|
| `store.go` | 双向行转换、上游错误哨兵映射、nil 网络配置处理、表与目录接口 |
| `cost_test.go` | 空基础价目表下，仅靠生成覆盖计算费用的闭环 |
| `store_test.go` | 错误映射、完整行往返、nil 网络配置与目录增删 |
