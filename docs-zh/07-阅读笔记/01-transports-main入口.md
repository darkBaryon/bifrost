# transports/bifrost-http/main.go 入口细读

已读到 `transports/bifrost-http/main.go` 完整入口,并顺着它看了 UI 嵌入、profiling、server 构造和 logger 注入点。当前结论:这个入口文件是典型的**薄 main**。它不直接做业务逻辑,只负责准备运行环境,然后把真正的系统装配交给 `server.Bootstrap(ctx)`。

## import 与包别名

`main.go` 里同时导入标准库、本项目模块和一个只为副作用导入的包:

```go
import (
    "context"
    "embed"
    "flag"
    "fmt"
    "os"
    "strings"
    "time"

    _ "go.uber.org/automaxprocs"

    bifrost "github.com/maximhq/bifrost/core"
    schemas "github.com/maximhq/bifrost/core/schemas"
    bifrostServer "github.com/maximhq/bifrost/transports/bifrost-http/server"
)
```

- `_ "go.uber.org/automaxprocs"`:只执行该包的 `init()` 副作用,用于在容器里按 cgroup CPU 限制自动设置 `GOMAXPROCS`。
- `bifrost "github.com/maximhq/bifrost/core"`:给 core 模块起别名,后面用 `bifrost.NewDefaultLogger(...)`。
- `bifrostServer ".../server"`:给 server 包起更明确的别名,避免裸 `server` 过于泛。

## 标准 context.Context 在这里做什么

入口里有两处标准库 context:

```go
ctx := context.Background()
err := server.Bootstrap(ctx)
```

这里的 `ctx` 是启动流程的根上下文。`context.Background()` 本身没有超时、不会自动取消、也没有携带值。它只是作为调用链起点传给 `Bootstrap`,再由 `Bootstrap` 继续传给配置加载、插件加载、core 初始化等下游过程。

另一个是关闭 profiling server 时:

```go
shutdownCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
defer cancel()
pprofServer.Shutdown(shutdownCtx)
```

这体现了标准 `context.Context` 的典型用途:给一次操作一个取消/超时信号。这里表示 pprof 优雅关闭最多等待 90 秒。

注意:标准 ctx 不是 DI 容器,也不会自动获得项目配置。ctx 里的取消、超时和值都来自上游代码显式创建和传递。谁拿到 ctx,谁可以通过 `Done()`、`Err()`、`Deadline()`、`Value()` 读取。

## embed:把前端 UI 打进二进制

`main.go` 的这两行:

```go
//go:embed all:ui
var uiContent embed.FS
```

表示把 `transports/bifrost-http/ui` 目录嵌入到 Go 二进制里。`embed.FS` 是一个只读的嵌入式文件系统,构建后的 `bifrost-http` 可执行文件内部就带着 `ui/index.html`、`ui/assets/...` 等静态资源。

传递链路:

```text
main.go
  uiContent embed.FS
    ↓
server.NewBifrostHTTPServer(Version, uiContent)
    ↓
BifrostHTTPServer.UIContent
    ↓
server.RegisterUIRoutes()
    ↓
handlers.NewUIHandler(s.UIContent, s.ShellRewriter)
    ↓
UIHandler.serveDashboard()
    ↓
h.uiContent.ReadFile(cleanPath)
```

也就是说,前端不是由 Node 服务在生产环境运行,而是先构建成静态文件,再由 Go HTTP 服务托管。

## 前后端同仓库的方式

本项目是 monorepo,前后端分目录开发:

```text
bifrost/
  core/        Go 核心库
  framework/   Go 框架/存储/工具
  plugins/     Go 插件
  transports/  Go HTTP 服务
  ui/          React/Vite 前端
```

生产发布的大致链路:

```text
1. 构建 ui/ 得到静态资源
2. 将产物放到 transports/bifrost-http/ui
3. 编译 transports/bifrost-http/main.go
4. go:embed 将 ui 静态资源打进 bifrost-http 二进制
```

开发模式下 `UIHandler` 还有一条旁路:如果 dev mode 开启,会优先把 UI 请求代理到本地 Vite dev server `localhost:3000`;代理失败再回退到 embedded UI。

## init() 为什么不全放 main()

`init()` 会在 `main()` 之前自动执行。本文件里的 `init()` 做两类轻量初始化:

```go
server = bifrostServer.NewBifrostHTTPServer(Version, uiContent)

flag.StringVar(&server.Port, "port", bifrostServer.DefaultPort, ...)
flag.StringVar(&server.Host, "host", defaultHost, ...)
```

它只是创建 server 对象并注册命令行 flags,没有启动网络服务、没有加载配置、没有做重活。真正读取命令行发生在 `main()` 的 `flag.Parse()`。

这里的分工可以理解为:

```text
init(): 声明程序有哪些参数,默认值来自哪里
main(): 解析参数,启动 profiling,设置 logger,执行 Bootstrap/Start
```

理论上这些也可以全放在 `main()`。把注册 flags 放在 `init()` 是 Go 项目里常见但有争议的写法:优点是包初始化时参数已经声明好;缺点是执行路径更隐式,读代码时要记得 `init()` 会自动跑。

## profiling 是什么

`main.go` 里:

```go
pprofServer := profiling.Start()
```

`profiling.Start()` 读取 `BIFROST_PPROF_PORT`。如果没有设置,直接返回 nil,完全不启动。如果设置了端口,它会启动一个独立的 pprof HTTP server,用于性能诊断。

主要接口:

```text
/debug/pprof/heap       堆内存
/debug/pprof/goroutine  goroutine
/debug/pprof/allocs     内存分配
/debug/pprof/block      阻塞
/debug/pprof/mutex      锁竞争
/debug/pprof/profile    CPU profile
/debug/pprof/trace      runtime trace
```

它默认绑定 `127.0.0.1`。如果通过 `BIFROST_PPROF_HOST` 暴露到非 loopback 地址,必须提供 `BIFROST_PPROF_USERNAME` 和 `BIFROST_PPROF_PASSWORD`,因为 pprof 可能暴露堆内存、请求内容、header、内部状态等敏感信息。

## logger 后续阅读点

`main.go` 先创建默认 logger:

```go
var logger = bifrost.NewDefaultLogger(schemas.LogLevelInfo)
```

随后根据 flags 调整输出格式和级别:

```go
logger.SetOutputType(schemas.LoggerOutputType(server.LogOutputStyle))
logger.SetLevel(schemas.LogLevel(server.LogLevel))
```

再通过包级 setter 传给几个 transport 包:

```go
lib.SetLogger(logger)
bifrostServer.SetLogger(logger)
handlers.SetLogger(logger)
```

后续阅读 logger 时建议从这几个位置开始:

- `core/schemas/logger.go`:先看 `schemas.Logger` 接口,理解项目希望 logger 提供哪些能力。
- `core/logger.go`:再看 `DefaultLogger` 的实现,包括日志级别、输出格式和并发安全。
- `transports/bifrost-http/{lib,server,handlers}/init.go`:看 transport 层如何通过包级变量接收 logger。

## 迷你样本:cmd/e2eseed(73 行看懂该模式)

```go
opts := seed.DefaultOptions()          // 默认值
fs.StringVar(&opts.ConfigDSN, ...)     // flags 覆盖
configDB, _ := seed.OpenDB(opts.ConfigDialect, opts.ConfigDSN)  // 构造依赖①
logsDB, _ := seed.OpenDB(opts.LogsDialect, opts.LogsDSN)        // 构造依赖②
seed.SeedBase(ctx, configDB, logsDB, opts)                      // 注入使用
```

flags → 构造 → 注入 → 干活,四步。主网关的 Bootstrap 只是这个 73 行样本的 40 倍放大版,模式完全相同。装配模式的原理分析见 [02-依赖注入模式.md](../03-源码精读/02-依赖注入模式.md)。
