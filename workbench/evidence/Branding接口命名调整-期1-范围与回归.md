# Branding 接口命名调整：范围与回归

起点：`/var/folders/m3/56qrlvk11xs7nc4cxjnw1b700000gn/T/branding-api-before-xeruces2` 中的 hashes.json 与源文件副本。使用文件 hash 对账，排除本轮开始前已有改动。

## 本轮更改

- `docs-zh/04-开发指南/07-ee包壳.md`
- `docs-zh/04-开发指南/08-Logo品牌设置.md`
- `ee/scripts/branding-smoke.py`
- `ee/scripts/smoke.sh`
- `ee/transports/bifrost-http/handlers/branding/handler.go`
- `ee/transports/bifrost-http/handlers/branding/settings.go`
- `ee/transports/bifrost-http/server/bootstrap.go`
- `ui/lib/store/apis/brandingApi.ts`
- `workbench/SUMMARY.md`
- `workbench/assets/nav.js`
- `workbench/cases/Logo品牌设置/期1/checklist.yaml`
- `workbench/index.md`
- `workbench/views/all.md`
- `workbench/需求报告.md`

## 本轮新增

- `ee/transports/bifrost-http/handlers/branding/branding_test.go`
- `ui/lib/store/apis/brandingApi.test.ts`
- `workbench/cases/Logo品牌设置/index.md`
- `workbench/evidence/Logo品牌设置-期1-接口命名调整检查清单.yaml`
- `workbench/cases/Logo品牌设置/期1/方案v4.md`
- `workbench/cases/Logo品牌设置/期1/方案v5.md`
- `workbench/cases/Logo品牌设置/期1/方案评审4.md`
- `workbench/cases/Logo品牌设置/期1/方案评审5.md`
- `workbench/reports/Logo品牌设置.md`

## 合并后删除

- `ee/transports/bifrost-http/handlers/branding/fixture_test.go`
- `ee/transports/bifrost-http/handlers/branding/handler_test.go`
- `ee/transports/bifrost-http/handlers/branding/settings_test.go`
- `ee/transports/bifrost-http/handlers/branding/validation_test.go`

## 回归证据

新增 TestBrandingActionRoutes 在旧实现上实际失败：POST /api/branding/get status 404；改路由后 handler 包全部通过。四份测试的原测试函数及断言均保留，修改请求方法/路径及响应局部命名，另补新路由方法约束和旧路径退出用例。

前端使用实际 RTK Query/createApi/fetchBaseQuery 生成请求，只替换 baseApi 的全局应用配置、fetch 网络边界；断言三条 URL、POST 方法和 update 请求体，3 例通过。加上原 hook 与图片测试共 16 例通过。这是客户端请求契约验证，不冒充浏览器交互。

旧路径终扫剩余为拒绝旧路由的测试、OSS HTML 回退探测、历史记录/发布日志和迁移说明。未改写上游发布日志。未运行依赖个人数据库的旧 smoke.sh 全套，只检查 shell 语法；真实企业路由、图片、鉴权和持久化由隔离 branding-smoke.py 验证。

独立代码评审补充调用方注释遗漏：ui/lib/hooks/useBranding.ts 仅更新 CACHE_KEY 上方说明（POST 查询、图片 GET/ETag 缓存验证），无逻辑变化；追加纳入范围。
