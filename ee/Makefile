# ee —— Bifrost 企业包壳. 用法: make -C ee <target>
# make -C ee 的 cwd 是 ee/, 涉及仓库根的命令一律先 cd $(ROOT)

ROOT := $(abspath ..)
APP_DIR ?= $(HOME)/.config/bifrost
PORT ?= 8080
VERSION ?= v2.0.0-ee.0
# 换前端只改 UI_DIST: 指向新前端的构建产物目录 (注意: make 变量赋值行尾不能带注释, 会把空格算进值里)
UI_DIST ?= $(ROOT)/ui/out
UI_EMBED := transports/bifrost-http/ui

.PHONY: help workspace ui-sync build-ui build dev smoke test

help: ## 列出目标
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-12s %s\n", $$1, $$2}'

workspace: ## 幂等地把 ./ee 加进仓库根的 go.work (上游 make dev 会重建 go.work 并抹掉它)
	@cd $(ROOT) && (grep -qxE '\s*\./ee' go.work || go work use ./ee)

ui-sync: ## 生成上游 ui/app/enterprise 覆盖层 (上游 make install-ui 会删掉它, 再跑本目标即可)
	@bash $(ROOT)/ee/ui/sync.sh

build-ui: ui-sync ## 构建 UI 并复制产物到 ee 的 embed 目录
	@test -d $(ROOT)/ui/node_modules || (echo "ui/node_modules missing: cd ui && npm ci"; exit 1)
	@cd $(ROOT)/ui && ./node_modules/.bin/vite build
	@test -f "$(UI_DIST)/index.html" || (echo "UI_DIST=$(UI_DIST) has no index.html; refusing to copy"; exit 1)
	@find "$(UI_EMBED)" -mindepth 1 -maxdepth 1 ! -name .keep -exec rm -rf {} +
	@cp -R "$(UI_DIST)/." "$(UI_EMBED)/"
	@echo "ui embedded from $(UI_DIST)"

build: workspace build-ui ## 编译带 UI 的 ee 版 bifrost-http 到 ee/tmp/
	@mkdir -p tmp
	@go build -ldflags "-X main.Version=$(VERSION)" -o tmp/bifrost-http ./transports/bifrost-http
	@echo "built ee/tmp/bifrost-http ($(VERSION))"

test: workspace ## go vet + go test
	@go vet ./... && go test ./...

dev: workspace ui-sync ## 从源码起 ee 版 API (无热重载); UI 开发服务器另起: cd ui && npm run dev (3000 → 8080)
	@go run ./transports/bifrost-http -port $(PORT) -app-dir $(APP_DIR) -log-style pretty

smoke: build ## 起 ee 与 OSS 各一份, 跑 T1-T7 断言 (见方案 §6)
	@bash $(ROOT)/ee/scripts/smoke.sh
