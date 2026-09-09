# Branding 布局整理：行为与范围对账

基线 commit：`8fd392958288016f5c97756cac7b5d5461699bca`。实际起点为当前分支原有未提交代码，其副本和全工作区文件哈希位于：

`/var/folders/m3/56qrlvk11xs7nc4cxjnw1b700000gn/T/bifrost-branding-layout-j09qzy0i`

## 代码对比

- Go：用 go/parser 读取搬移前后声明，忽略注释与 import，仅将迁移后的表类型包限定名统一，再用 AST 打印对比。handler 的 22 份声明、store/table/migration 的 19 份声明全部一致，包含原有测试及断言。
- 前端：用 TypeScript AST 对比，原页面的 24 条表单状态/操作声明顺序与新 hook 一致；readImage、failureMessage 函数体一致；移除按钮回调原样移入 remove，加载态和完整 JSX 一致。
- 对比脚本：`/tmp/compare_branding.go`、`/tmp/compare_branding_ui.cjs`，从仓库根执行。Go 参数为上述基线目录和仓库根；JS 自动读取 `/tmp/bifrost-branding-layout-current`。
- 额外读图测试为 8 例，验证异步成功、读取/解码失败和单边/总像素边界。浏览器 API 为替身，不声称已做浏览器 UI 实测。

## 范围核对

按起点全工作区哈希逐文件比对，变更全部位于本轮声明范围。上游 `ui/`、`core/`、`framework/`、`transports/`、`plugins/` 源码未被本轮改动。原有 probe 行为未变，只订正一处过期目录注释。

本轮路径：

- `docs-zh/04-开发指南/07-ee包壳.md`
- `docs-zh/04-开发指南/08-Logo品牌设置.md`
- `ee/framework/configstore/branding.go`
- `ee/framework/configstore/branding/migration.go`
- `ee/framework/configstore/branding/migration_test.go`
- `ee/framework/configstore/branding/store.go`
- `ee/framework/configstore/branding/store_test.go`
- `ee/framework/configstore/branding/table.go`
- `ee/framework/configstore/branding_test.go`
- `ee/framework/configstore/migrations.go`
- `ee/framework/configstore/migrations_test.go`
- `ee/framework/configstore/tables/branding.go`
- `ee/transports/bifrost-http/handlers/branding.go`
- `ee/transports/bifrost-http/handlers/branding/asset.go`
- `ee/transports/bifrost-http/handlers/branding/fixture_test.go`
- `ee/transports/bifrost-http/handlers/branding/handler.go`
- `ee/transports/bifrost-http/handlers/branding/handler_test.go`
- `ee/transports/bifrost-http/handlers/branding/settings.go`
- `ee/transports/bifrost-http/handlers/branding/settings_test.go`
- `ee/transports/bifrost-http/handlers/branding/validation.go`
- `ee/transports/bifrost-http/handlers/branding/validation_test.go`
- `ee/transports/bifrost-http/handlers/branding_test.go`
- `ee/transports/bifrost-http/lib/tables.go`
- `ee/transports/bifrost-http/server/bootstrap.go`
- `ee/ui/app/enterprise/components/branding/brandingPreview.tsx`
- `ee/ui/app/enterprise/components/branding/brandingUpload.tsx`
- `ee/ui/app/enterprise/components/branding/brandingView.tsx`
- `ee/ui/app/enterprise/components/branding/image.test.ts`
- `ee/ui/app/enterprise/components/branding/image.ts`
- `ee/ui/app/enterprise/components/branding/useBrandingForm.ts`
- `ee/ui/app/enterprise/lib/schemas/branding.ts`
- `workbench/SUMMARY.md`
- `workbench/assets/nav.js`
- `workbench/cases/Logo品牌设置/index.md`
- `workbench/evidence/Logo品牌设置-期1-代码布局整理检查清单.yaml`
- `workbench/cases/Logo品牌设置/期1/方案v3.md`
- `workbench/cases/Logo品牌设置/期1/方案评审3.md`
- `workbench/cases/Logo品牌设置/期1/checklist.yaml`
- `workbench/evidence/Branding代码布局整理-期1-预飞.md`
- `workbench/index.md`
- `workbench/reports/Logo品牌设置.md`
- `workbench/reports/Logo品牌设置.md`
- `workbench/views/all.md`
- `workbench/需求报告.md`
