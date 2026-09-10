# EE 程序入口

本包创建 Bifrost HTTP Server，调用 [app](../../internal/app/README.md) 装配 EE，再启动服务。

| 文件 | 职责 |
|---|---|
| [main.go](main.go) | 命令行、日志、UI embed、应用启动与 profiling 关闭 |
| `ui/` | 构建时嵌入的前端产物，源代码位于 `ee/ui/` 及根 `ui/` |

在仓库根运行 `make -C ee build`，产物为 `ee/tmp/bifrost-http`。入口仍复用上游启动行为，上游 main 改动时需核对本文件。
