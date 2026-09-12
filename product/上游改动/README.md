# 上游改动登记

[← 返回产品与技术文档](../README.md)

本目录登记我们在 fork 中对**上游文件**做的全部修改。上游文件指 `core/`、`framework/`、`transports/`、`ui/`、`plugins/` 下的代码，它们由 maximhq/bifrost 维护，我们每次同步上游都要重新合并。`ee/`、`product/`、`workbench/`、`docs-zh/` 是我们自己的目录，不属于登记范围。

登记的目的只有一个：**同步上游前先看这份清单**，知道哪些文件会冲突、每处改动当初为什么必须改、合并后要验证什么。

## 什么时候允许改上游文件

默认不改。EE 功能优先用 `ee/` 内的生成或注入机制实现，例如 `ee/ui/sync.sh` 生成 `ui/app/enterprise` 覆盖层，而不是直接编辑上游组件。

确实改不了的，按下面三条办：

1. 在工程方案里单列一节写明改动范围、接口签名和执行时点，随 Gate 1 一起批准。账号认证期 1 的[方案 §4.5「宿主改动白名单与兼容矩阵」](../../workbench/cases/账号认证/期1/方案v2.md)是现成样板。
2. 改动必须是**纯增量**：新增可选扩展点，默认值保持上游原行为，不改写上游已有算法。
3. 实施中发现方案没覆盖、但只能在上游修的问题，按偏差登记，并在代码评审中单独复核。
4. 合入后回到本文件补登记。

## 判定基线

上游合并基点：`c05bc0e2118532bc0a3aecc158b3431e36764206`（`docs: github copilot provider (#6357)`）。

重新生成清单：

```bash
git fetch upstream
git diff --numstat $(git merge-base develop upstream/dev) develop -- \
  core/ framework/ transports/ ui/ plugins/
```

## 修改上游已有文件（7 个）

这些是同步上游时**会产生冲突**的文件。

| 文件 | 增/删 | 改了什么 | 依据 |
|---|---|---|---|
| [transports/bifrost-http/server/server.go](../../transports/bifrost-http/server/server.go) | +36/-3 | 控制台认证扩展点 | 方案 §4.5 |
| [transports/bifrost-http/handlers/websocket.go](../../transports/bifrost-http/handlers/websocket.go) | +32/-6 | WebSocket 连接重验回调 | 方案 §4.5 |
| [transports/bifrost-http/handlers/middlewares.go](../../transports/bifrost-http/handlers/middlewares.go) | +10/-1 | 访问日志不记 WS 票据 | 偏差 R3 |
| [ui/app/globals.css](../../ui/app/globals.css) | +2/-0 | Tailwind 扫描 EE 界面源码 | 品牌定制期 1 |
| [ui/app/main.tsx](../../ui/app/main.tsx) | +2/-0 | 安装运行时中文化 | UI 中文化期 1 |
| [ui/lib/hooks/useBranding.ts](../../ui/lib/hooks/useBranding.ts) | +20/-18 | 品牌槽位回退规则 | 品牌定制期 1 |
| [ui/lib/store/apis/brandingApi.ts](../../ui/lib/store/apis/brandingApi.ts) | +10/-16 | 品牌接口改 POST | 品牌定制期 1 |

### server.go：控制台认证扩展点

新增 `ConsoleAuthProvider` 接口和 `BifrostHTTPServer.ConsoleAuthFactory` 可选字段。`Bootstrap` 在认证依赖就绪后、`RegisterAPIRoutes` 之前调用工厂，用返回的 provider 替换管理面鉴权中间件；`RegisterAPIRoutes` 中的会话路由注册也改为优先用 provider。工厂为 nil 时全部走原分支，OSS 行为不变。

必须改上游的原因：EE 要接管**所有**管理路由的鉴权，包括上游后续新增的路由。在 EE 侧维护路由名单会随上游演进失效。

推理链（`inferenceMiddlewares`、VK 鉴权、TempTokens 装配）一行未动。

冲突风险高：`Bootstrap` 是上游活跃函数，改动分散在 4 处。

### websocket.go：连接重验回调

新增 `WebSocketAuthorizeContextKey` 常量，值为 `func(context.Context) error`。EE 管理中间件通过验证后写入只捕获 session ID 的闭包，`connectStream` 在 hijack 前把它复制到 client，`writeSafely` 在每次写入前调用，2 秒超时，失败即关连接。未设置回调时走上游原行为。

同一改动里修了两个上游缺陷：`connectStream` 原先在 hijack 回调内读 `RequestCtx`，而该对象此时已被复用；pong handler 续期未持客户端锁。失效时还要显式设置底层读 deadline，因为 fasthttp 的 `hijackConn.Close` 在 handler 返回前不真正关 socket。

必须改上游的原因：WebSocket 连接建立后脱离中间件链，EE 侧没有任何位置能对已建立的连接做重新鉴权。

冲突风险高：`connectStream` 和 `writeSafely` 都是上游活跃函数。

### middlewares.go：访问日志不记 WS 票据

新增非导出函数 `requestLogTarget`，访问日志生成 `http.target` 时对 `/ws` 只记录路径，不记 query。不改变请求本身，票据仍按原协议读取和消费。同时新增 [wslogtarget_test.go](../../transports/bifrost-http/handlers/wslogtarget_test.go) 回归。

这条不在原白名单内，是代码评审 1 的 R3 发现后登记的偏差：外层 gzip 解压提前返回时，完整 ticket 会写进 `server.log`，而该票据之后仍能正常握手。日志在 CORS 中间件的 defer 里生成，位于解压和 Router 之外，EE 中间件在更内层，盖不住这条路径。

冲突风险中：改动是 1 行调用加 1 个新函数。

### ui/app/globals.css：Tailwind 扫描 EE 界面源码

新增一行 `@source "../../ee/ui/app/enterprise";`。

必须改上游的原因：Tailwind 的扫描目录只能写在 `globals.css` 里，而 `ee/ui/sync.sh` 生成的覆盖层是符号链接，扫描器不跟随，EE 自有界面的样式类不会被生成。

冲突风险低：追加一行，不改上游已有行。

### ui/app/main.tsx：安装运行时中文化

新增 `installZhLocale` 的 import 和一行调用，位置在 `installVersionSkewListeners()` 之后。

必须改上游的原因：运行时中文化需要在 React 挂载前接管文案，`main.tsx` 是唯一的入口。

冲突风险低：追加两行。

### useBranding.ts / brandingApi.ts：品牌前端适配

`brandingApi.ts` 把三个品牌接口从 `GET/PUT/DELETE /branding` 改为 `POST /api/branding/{get,update,reset}`，对齐我们的[接口命名规范](../../workbench/规范/项目/编码规范.md)。`useBranding.ts` 抽出 `pickBrandingSources` 供设置页预览复用同一套槽位回退规则（图标缺省时回退到自定义 logo，再回退到内置图标），两个文件的英文注释改为中文。配套新增 `useBranding.test.ts`、`brandingApi.test.ts`。

冲突风险中：上游这两个文件属于其企业版分支，改动频率未知；我们改写了函数体和注释，不是纯追加。

## 新增文件（7 个）

放在上游目录下、但上游没有的文件。同步上游时不会冲突，除非上游新增同名文件。

| 文件 | 行数 | 用途 |
|---|---|---|
| transports/bifrost-http/handlers/wslogtarget_test.go | 22 | `requestLogTarget` 回归 |
| ui/lib/hooks/useBranding.test.ts | 61 | 品牌槽位回退规则测试 |
| ui/lib/store/apis/brandingApi.test.ts | 65 | 品牌接口契约测试 |
| ui/lib/zhDict.json | 3049 | 中文词条数据 |
| ui/lib/zhLocale.ts | 174 | 运行时中文化实现 |
| ui/scripts/zh-coverage.mjs | 444 | 中文化覆盖率扫描 |
| ui/scripts/zh-coverage-whitelist.txt | 530 | 覆盖率扫描白名单 |

`ui/app/enterprise/` 不在此列：它由 `ee/ui/sync.sh` 生成，不进版本库。上游 `make install-ui` 会删除该目录，重跑脚本即可。

## 同步上游时怎么做

1. `git fetch upstream`，合并前先读本文件，确认 7 个修改文件的当前状态。
2. 合并冲突时，以本文件记录的**意图**为准重新落地，不要机械接受任何一方。三处 Go 改动都是「新增可选扩展点、默认保持上游行为」，只要这个性质还成立，实现可以跟着上游重写。
3. 合并后按顺序验证：
   - `cd ee && GOWORK=off go vet ./... && go test ./...`
   - EE 启动后 `/api/config` 返回 EE 投影、管理接口需登录、`/ws` 握手正常且断会话后推送被拒
   - 上游 `go test ./transports/...`，确认 `wslogtarget_test.go` 仍通过
   - 前端 `ui/` 下跑 `zh-coverage.mjs`，确认中文化未因上游新增文案退化
   - 对照 `transports/bifrost-http/handlers/temptokens.go` 里三个 scope 的 `AllowedRoutes`，确认 EE 的 `temporaryRoute()`（宿主认证接入包）仍逐条覆盖；上游加了 scope 而 EE 没跟上，那条流程会被 401 挡掉
4. 上游若自行实现了同类扩展点（例如官方支持替换控制台认证），删除我们的对应改动，改用官方接口，并在此登记。
5. 本期改动的完整背景见 [账号认证期 1 方案](../../workbench/cases/账号认证/期1/方案v2.md) §4.5 与 [验收记录](../../workbench/cases/账号认证/期1/验收记录.md)。
