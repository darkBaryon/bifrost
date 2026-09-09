# Logo品牌设置 期1 独立测试

执行职责：branding_independent_test，未参与实施；重读实际代码、用例与方案后独立执行，不继承开发自测结论。
时间：2026-09-08 21:52–21:54（Asia/Shanghai）。

## 结论

本轮实际执行的 SQLite/HTTP 自动化、真实二进制隔离 smoke、共享 hook 回退测试通过。**完整 L3 验收仍阻塞**：PostgreSQL 无隔离实例，未验证事务锁、并发迁移及超时/取消；浏览器文件上传和完整交互本职责未执行。不得据本证据将 Gate 2 判为通过。

## 测试对象与准入

- 工作目录：`/Users/xinyue/VSCode/ws_2026/bifrost-branding`。
- 实核分支 `feat/branding`，HEAD/基线 `8fd392958288016f5c97756cac7b5d5461699bca`；对象为未暂存工作树，遵守用户不提交例外。
- 已读根 AGENTS、workbench AGENTS、流程、测试职责规范、需求、方案 v2、checklist，以及品牌 Store/迁移/表/Handler/装配/Go 测试/smoke/hook 测试。
- 平台：macOS 26.4.1 arm64；Go 1.27.1 darwin/arm64；Node v26.5.0；Python 3.13.5；Vitest 4.1.6。
- 二进制为现有 `ee/tmp/bifrost-branding`，本职责未重新构建；因此二进制行为结论绑定下列二进制哈希，Go 用例结论绑定源文件哈希，不将二者构建对应关系当作本职责已证实。
- Go 用例自行创建临时 SQLite 文件；smoke 使用新临时 app-dir、随机管理员、无 provider 配置、随机 localhost 端口，停止/重启仅针对它自行创建的 PID。未读取个人数据库，未安装或连接 PostgreSQL，未启动浏览器。

## 实际命令与结果

| 编号 | 命令（工作目录） | 实际结果 |
|---|---|---|
| T1 | `GOWORK=off go test -timeout 60s ./...`（ee） | exit 0，但结果 cached，单独不足以证明本轮执行 |
| T2 | `GOWORK=off go test -count=1 -timeout 60s ./...`（ee） | exit 0；configstore 0.068s、handlers 0.346s、lib 0.019s；其他 3 包无测试 |
| T3 | `./node_modules/.bin/vitest run lib/hooks/useBranding.test.ts`（ui） | exit 0，1 文件 5 例通过，204ms |
| T4 | `python3 ee/scripts/branding-smoke.py --binary ee/tmp/bifrost-branding`（根） | exit 0，真实认证/图片缓存/拒绝原子性/保存后重启/reset 后再重启通过 |

T2 原始摘要：

```text
ok github.com/darkBaryon/bifrost/ee/framework/configstore 0.068s
?  github.com/darkBaryon/bifrost/ee/framework/configstore/tables [no test files]
?  github.com/darkBaryon/bifrost/ee/transports/bifrost-http [no test files]
ok github.com/darkBaryon/bifrost/ee/transports/bifrost-http/handlers 0.346s
ok github.com/darkBaryon/bifrost/ee/transports/bifrost-http/lib 0.019s
?  github.com/darkBaryon/bifrost/ee/transports/bifrost-http/server [no test files]
```

T4 原始输出：

```text
PASS: real auth (cookie/basic/bearer), image bytes/cache, atomic rejection, restart retention, reset + second restart
```

## 用例覆盖与限制

| 范围 | 实际操作、断言与结果 | 结论 |
|---|---|---|
| SQLite 持久化 | 初始空表；并发分别写 Logo/Icon 保留两槽；重复迁移；关闭并重开文件 DB；Logo 字节及双 hash 保留；清 Icon 保留 Logo；重复 Reset | 通过 |
| SQLite 迁移原子性 | 种子创建与 migration 版本记录两阶段分别注入错误，迁移失败后品牌表不存在；撤销故障后重试及重复迁移，版本计数为 1 | 通过 |
| Store 写事务 | 注入事务内查询失败，Update 返回错误；撤销故障后对比旧 Logo 字节、hash、UpdatedAt 不变 | 通过 |
| HTTP 图片契约 | PNG data URI 保存和字节/MIME/nosniff、JPEG 独立 Icon、清 Icon、ETag 304、Reset 后旧 ETag URL 为 404 | 通过 |
| 非法输入 | 空对象/null/数组/未知字段/非字符串/孤立 MIME/坏 base64/伪 MIME/双图一坏/多个 JSON/SVG 400 且旧状态相同；body/编码长度超限 413；4097px 与截断 PNG 400 | 通过 |
| 数据库读错误 | 关闭连接后 GET 返回 500，不伪装默认状态 | 通过 |
| 真实管理认证 | Go 使用 APIMiddleware，匿名/VK/错误 Basic 对 PUT/DELETE 均 401，有效 Basic 可写；smoke 再测匿名/VK/无效 Bearer 拒绝及 Cookie、Basic、Bearer 成功写 | 通过 |
| 真实重启 | 新二进制进程保存 PNG+JPEG，匿名取得两图并逐字节比较且 ETag 304；同 app-dir 重启后整个公开状态（含 URL/updated_at）相同，Logo 字节相同；Reset 后旧 Logo URL 404，第二次重启仍 disabled | 通过；重启后未逐字节复核 Icon |
| 共享 hook | 两主题 Logo 兼作 Icon；独立 Icon 优先/仅 Icon；Reset 清 localStorage 并恢复主题默认；OSS 忽略 ee 缓存 | 通过；React/Query 为 mock，不是 DOM/E2E |
| PostgreSQL | 无本机隔离实例，不安装、不连其他库 | 阻塞 |
| 浏览器上传/完整交互 | 未起浏览器；已知文件上传被扩展拒绝，但本职责未重现。其他职责截图/自测不计入本轮通过 | 未执行 |
| 其余方案反例 | SQLite busy 专门竞争/重试；真正过期 token（无效 token 不等于过期）；HTTP 写失败的 500 与回滚；提交失败；提交后断网再读；总像素>400万边界；GIF/WebP；独立启动失败不监听 | 未执行（部分逻辑可读，不等于实测） |
| 构建/全预飞 | 本职责未运行 build/vet/typecheck/格式和完整 preflight；不继承实施者结论 | 未执行 |

`server/bootstrap_test.go` 当前不存在，server 包明确报告无测试；正常装配由上述真实 smoke 覆盖，错误装配分支没有独立自动化证据。
本轮未发现已执行用例的失败；未改生产文件、测试、视图，未暂存、提交或推送。

## 测试对象 SHA-256

以下哈希于执行结束时读取。若其后对象变化，需针对影响范围复测；不覆盖本轮证据。

```text
b5becb961279d5fa77962cb2c49a534087589d62167c5dd7bc757352cfbeb385  ee/framework/configstore/branding.go
754a7173a9628729342ffe54b38a70e0c411471d1731d48159d6df0d0f3bcdd9  ee/framework/configstore/branding_test.go
383d3040ff4f1fa0cb6f0ecc190fd4a306c82d53a3f8cc6c963241b348cbf502  ee/framework/configstore/migrations.go
dbb4ef7c2ca95c36e9745869edb157f9c1d75e3a4d25746eddf9e7093e62da5b  ee/framework/configstore/migrations_test.go
2cbebbccf31076010dd600e7ae79bba778fa394cfaacf1eeb1a56142744b8dd5  ee/framework/configstore/tables/branding.go
8f4ce39b93b99ebb94ee8b3f0aee7f30d8074e25f362b09a68c1b6f60154b7e0  ee/transports/bifrost-http/handlers/branding.go
4d7d84b1be87acef0d65a82cb996a514a8455a3e9020a6baef80b88cc41fd55b  ee/transports/bifrost-http/handlers/branding_test.go
aa8fcb3138f959cec440337915cbe22487f5d9ea075dba9864b3b36c3d0742ea  ee/transports/bifrost-http/server/bootstrap.go
ad561fed796366fe4eed3e99c5e11ad2734975dacdebe7694109a4b4639252e4  ee/scripts/branding-smoke.py
1ef627bceab88c2266f66fb3ca3c0507ed3f9bc70020277b0b9dae50b7d907bc  ui/lib/hooks/useBranding.ts
4e47d04897417d05afa64114f5a708bf51d641799338643a41726ef40aae04ca  ui/lib/hooks/useBranding.test.ts
de23909a69a424aa62022c74586db499787fb5b6c8c4b0fe04a705a430008807  ee/tmp/bifrost-branding
```

## 第二轮：补充测试差量复测

时间：2026-09-08T21:54:56.852754+08:00。
第一轮正文与哈希保留为历史；本节是最新差量结论。实施职责只补充 `migrations_test.go` 和 `branding-smoke.py`，测试职责亲自重读新增测试后执行；未修改被测生产代码。二进制身份见下表。

- `cd ee && GOWORK=off go test -count=1 -timeout 60s ./...`：exit 0。configstore 0.087s、handlers 0.372s、lib 0.021s，另 3 包无测试。
- `python3 ee/scripts/branding-smoke.py --binary ee/tmp/bifrost-branding`：exit 0，输出仍为 `PASS: real auth (cookie/basic/bearer), image bytes/cache, atomic rejection, restart retention, reset + second restart`；虽摘要未新增 expired 字样，代码中的过期断言已在本轮执行。
- UI 文件无变化，本轮未重复 UI；第一轮 5 例回退结果保留。

| 新增覆盖 | 操作与结果 | 结论 |
|---|---|---|
| SQLite 写锁竞争/重试 | 第一连接 BEGIN IMMEDIATE，第二连接 busy_timeout=20，迁移返回错误；释放锁后确认没有残留品牌表；重试成功、版本计数 1 | 通过 |
| SQLite 取消 | 预先取消 context，迁移返回 context.Canceled、未创建品牌表；新 context 重试成功 | 通过；不等同于等待数据库锁时中途取消 |
| 真实会话过期 | 真实登录取得 token，仅在本 smoke 私有临时 SQLite 将 sessions.expires_at 置为过去；旧 Cookie 与旧 Bearer 的 PUT/DELETE 都返回 401；重新登录后正常写入，后续缓存和重启全流程通过 | 通过 |

因此第一轮「SQLite busy 专门竞争/重试」「真正过期 token」两项未执行记录，已被本轮通过结果解除。其余未执行项继续有效：HTTP 写失败 500 与回滚、提交失败/提交后断网、总像素上界/GIF/WebP专门反例、装配失败不监听、重启后 Icon 逐字节比较，以及本职责未独立构建/全预飞。**PostgreSQL 仍阻塞；浏览器仍未执行，上传权限阻塞未解除；完整 Gate 2 不通过。**

第二轮测试对象 SHA-256：

```text
dbb4ef7c2ca95c36e9745869edb157f9c1d75e3a4d25746eddf9e7093e62da5b  ee/framework/configstore/migrations_test.go
ad561fed796366fe4eed3e99c5e11ad2734975dacdebe7694109a4b4639252e4  ee/scripts/branding-smoke.py
de23909a69a424aa62022c74586db499787fb5b6c8c4b0fe04a705a430008807  ee/tmp/bifrost-branding
```
