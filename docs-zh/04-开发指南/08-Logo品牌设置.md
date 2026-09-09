# Logo 品牌设置

企业覆盖层的 Settings → Branding 提供 Logo 与选填小图标上传、预览、保存和恢复默认。图片存入当前配置数据库的 `ee_branding` 表，服务重启后保留；不依赖外部图片托管。

## 使用

1. 登录管理端，进入 Branding，选择 PNG 或 JPEG。每张 ≤1 MiB，单边 ≤4096 像素且总像素 ≤400 万。
2. 检查浅色、深色背景下的登录页和侧栏预览。自定义图片不区分主题；小图标留空时折叠侧栏按比例沿用 Logo。
3. 点击保存后生效。其他已打开页面刷新后更新。上传、移除操作在保存前仅影响草稿，离开页面不保留草稿。
4. “恢复默认”确认后立即清空两份覆盖，也丢弃草稿；取消确认不改动。全部为空时使用随主题变化的 Bifrost 默认图片。

失败时草稿保留；网络中断可能发生在服务器提交之后，请先“重新读取”核对，再重试。读取失败时禁止修改。首次加载可能短暂显示默认图或浏览器上次缓存，不承诺零闪烁。

登录表单、登录等待卡、展开/折叠侧栏复用现有品牌 hook；已有 topbar 和更新等待页也会跟随它。产品名、favicon、配色、Powered by 文案均不在本功能范围。

## API

| 方法 | 路径 | 行为 |
|---|---|---|
| POST | `/api/branding/get` | 公开品牌状态，供未登录页面读取 |
| POST | `/api/branding/update` | 管理端认证；按槽位合并保存 |
| POST | `/api/branding/reset` | 管理端认证；恢复默认，幂等 |
| GET | `/api/branding/assets/{logo或icon}/{sha256}` | 公开当前版本图片；未知或旧版本返回 404 |

`POST /api/branding/update` 的请求体为 JSON：`logo`/`icon` 可使用标准 base64 或 `data:image/png;base64,...`；`logo_mime`/`icon_mime` 可省略，提供时必须与图片内容一致。省略槽位保留，空字符串清除，null 拒绝；MIME 不能独立提交。空对象、未知字段、错误格式、损坏图片均 400，单图或请求超限 413，存储错误 500；不返回底层数据库错误。

状态字段：`enabled`、`has_logo`、`has_icon`，配置后提供 `logo_url`/`icon_url` 和 `updated_at`。`has_icon` 表示实际独立小图标，Logo 回退在前端完成。Logo 与 Icon 原子更新，单槽更新只写显式槽位；同槽并发以最后提交为准。

公开 JSON 使用 `Cache-Control: no-store`；图片使用内容 hash URL、ETag、`nosniff` 和 `max-age=0, must-revalidate`。仅当当前槽位/版本仍存在时才可返回 304。重置后旧 URL 404，但无法追回浏览器已持有的历史副本。图片本身属于公开品牌素材。

写操作沿用现有 APIMiddleware，不新增 RBAC。**认证关闭时，或运维把路径加入管理接口白名单时，沿用其他管理接口的放行行为**。需要管理员保护的部署应启用现有认证，且不把品牌写路径加入白名单。

handler 在解码前限制请求体 3 MiB；fasthttp 的接收上限仍由已有 `client.max_request_body_size_mb` 控制，默认 100 MiB，若设得更小可能先拒绝请求。本功能不修改全局接收配置。

## 存储与回滚

启动通过现有 `ConfigStore.RunMigration` 取得迁移连接，运行版本 `ee_branding_v1`，与上游共用 migrations 版本表；只新增本案表。外层事务负责表、空单例和迁移版本共同提交/回滚。PostgreSQL 使用本功能事务锁及 10 秒锁超时；这不等于整个 ee 的公共迁移协调已完成。

启动迁移失败则不监听，可查部署日志排查；运行中数据库失败不冒充空配置。旧程序可以忽略新表，回退程序时优先保留图片数据。删除表会丢失素材，不自动执行；先备份配置数据库，获授权后再做数据级回滚。

## 开发验证

在仓库根执行：

```sh
make -C ee build-ui
cd ee
GOWORK=off go vet ./...
GOWORK=off go test -timeout 60s ./...
GOWORK=off go build -o tmp/bifrost-branding ./transports/bifrost-http
cd ..
python3 ee/scripts/branding-smoke.py --binary ee/tmp/bifrost-branding
```

新 smoke 使用空白临时配置和数据库、临时管理员、动态端口，只管理自身子进程；验证真实登录、公开图、失败原子性、保存后重启及重置后重启。不使用依赖个人数据库的旧 `ee/scripts/smoke.sh`。

前端：先构建生成路由，再在 ui 下运行 `tsc --noEmit`、`tsc --noEmit -p ../ee/ui/tsconfig.json --types vite/client,node`、`vitest run lib/hooks/useBranding.test.ts`。ee 覆盖源新增了上游 API imports，独立 ee 类型检查需显式带 Node 环境类型以解析上游 process 声明，不修改共享 tsconfig。

SQLite/二进制验证与 PostgreSQL 迁移验证必须分别记录；浏览器交互不以构建或单测替代。具体执行结果见 workbench 的 Logo品牌设置案。


## 代码位置

- `ee/transports/bifrost-http/handlers/branding/`：`handler.go` 注册路由，`settings.go` 处理设置读写与响应，`validation.go` 校验输入，`asset.go` 投递图片。
- `ee/framework/configstore/branding/`：`store.go` 处理持久化，`migration.go` 处理版本化迁移，`table.go` 定义 `ee_branding` 表。
- `ee/ui/app/enterprise/components/branding/`：页面、上传控件和预览各自一份文件；`useBrandingForm.ts` 管理草稿与操作，`image.ts` 读取和校验图片。
- `ee/ui/app/enterprise/lib/schemas/branding.ts`：保留企业 schema 位置，集中定义上传和表单规则。

布局整理保留了迁移版本和图片保存格式，过程记录见 [Logo品牌设置的评审修改记录](../../workbench/reports/Logo品牌设置.md)。

接口命名调整后，查询、保存和重置统一使用上述 POST 路径，旧 GET/PUT/DELETE `/api/branding` 不再提供。前后端应一起更新；图片仍通过 GET 读取。handler 测试集中在 `branding/branding_test.go`。

## 上传错误与样式排查

选图后显示有效草稿的文件名，保存后显示“已设置图片”，移除后显示“未选择图片”。任一图片校验错误都会阻止保存；重新选择有效图片或移除该位置可清除错误，没有旧图时也允许移除清错。

品牌页面使用的 Tailwind 类由 `ui/app/globals.css` 中 `@source "../../ee/ui/app/enterprise"` 显式扫描。若升级上游后出现样式缺失，先检查该声明、相对路径及 `make -C ee build-ui` 的产物；不通过生成 `.ignore` 或修改 Git 本机配置处理。

PostgreSQL测试入口、专用测试库与schema隔离约定见[存储包README](../../ee/framework/configstore/branding/README.md#数据库测试)。
