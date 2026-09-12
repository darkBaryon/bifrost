# 国内定价

根包只依赖标准库（后续稳定 ID 使用已存在的 google/uuid），负责国内官网价格及规则；宿主与存储类型留在适配包。

| 文件 | 职责 |
|---|---|
| `model.go` | 无 JSON/数据库标签的价格业务模型与币种、取价口径 |
| `pricefile.go` | 独立 JSON DTO、嵌入文件、解析及整份校验 |
| `pricefile_test.go` | 文件契约与非法输入测试 |
| `data/cn-prices.json` | 随版本发布的价格目录；当前步骤为空骨架 |
