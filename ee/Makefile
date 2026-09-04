# ee —— Bifrost 企业包壳. 用法: make -C ee <target>
# 目标在仓库根执行 (make -C ee 的 cwd 是 ee/, 所以每条先 cd ..)

ROOT := $(abspath ..)
APP_DIR ?= $(HOME)/.config/bifrost
PORT ?= 8080
VERSION ?= v2.0.0-ee.0
UI_DIST ?= $(ROOT)/ui/out

.PHONY: workspace build dev help

help: ## 列出目标
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-12s %s\n", $$1, $$2}'

workspace: ## 幂等地把 ./ee 加进仓库根的 go.work (上游 make dev 会重建 go.work 并抹掉它)
	@cd $(ROOT) && (grep -qxE '\s*\./ee' go.work || go work use ./ee)

build: workspace ## 编译 ee 版 bifrost-http 到 ee/tmp/ (UI 产物需先 make build-ui)
	@mkdir -p tmp
	@cd $(ROOT)/ee && go build -ldflags "-X main.Version=$(VERSION)" -o tmp/bifrost-http ./transports/bifrost-http
	@echo "built ee/tmp/bifrost-http ($(VERSION))"

dev: workspace ## 从源码起 ee 版 API (无热重载); UI 开发服务器另起: cd ui && npm run dev
	@cd $(ROOT)/ee && go run ./transports/bifrost-http -port $(PORT) -app-dir $(APP_DIR) -log-style pretty
