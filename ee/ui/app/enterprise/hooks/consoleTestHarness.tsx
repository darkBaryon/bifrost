// 本文件给基础查询测试提供真实 RTK store 与无浏览器的 hook 渲染入口。
import { baseApi } from "@/lib/store/apis/baseApi";
import type { ConsoleConfig } from "@/lib/types/console";
import type { BifrostConfig } from "@/lib/types/config";
import { configureStore } from "@reduxjs/toolkit";
import type { ApiEndpointQuery, BaseQueryFn, QueryDefinition } from "@reduxjs/toolkit/query";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { Provider } from "react-redux";
import { afterEach, vi } from "vitest";
import "./useConsoleConfig";

type BootstrapDefinition = QueryDefinition<void, BaseQueryFn, "Config", ConsoleConfig, "api">;
// 生产 API 保持私有；测试只取得注册后的真实端点，避免额外导出测试专用入口。
export const bootstrapEndpoint = (baseApi.endpoints as { getConsoleBootstrap: ApiEndpointQuery<BootstrapDefinition, {}> })
	.getConsoleBootstrap;

// coreConfigFixture 包含基础状态之外的设置字段，用于发现两个缓存之间的污染。
export const coreConfigFixture = {
	is_db_connected: true,
	is_logs_connected: true,
	is_cache_connected: false,
	is_git_available: false,
	env_label: "OSS",
	client_config: {},
	framework_config: {},
	auth_config: { is_enabled: true },
	restart_required: { required: true, reason: "OSS restart reason" },
} as BifrostConfig;

// setupConsoleStore 只替换传输边界 fetch，保留生产 baseApi 的凭据、错误和缓存处理。
export function setupConsoleStore(respond: (request: Request) => Response | Promise<Response>) {
	const requests: Request[] = [];
	vi.stubGlobal("fetch", async (request: Request) => {
		requests.push(request);
		return respond(request);
	});
	const store = configureStore({
		reducer: { [baseApi.reducerPath]: baseApi.reducer },
		middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(baseApi.middleware),
	});
	afterEach(() => store.dispatch(baseApi.util.resetApiState()));
	return { store, requests };
}

// jsonResponse 构造不依赖网络服务的真实 Fetch Response。
export function jsonResponse(data: unknown, status = 200) {
	return new Response(JSON.stringify(data), { status, headers: { "Content-Type": "application/json" } });
}

// renderHookValue 在服务器渲染中读取真实 hook 的缓存/skip 状态，不模拟 React hooks。
export function renderHookValue<T>(store: ReturnType<typeof setupConsoleStore>["store"], useValue: () => T): T {
	let value: T;
	function Probe() {
		value = useValue();
		return null;
	}
	renderToStaticMarkup(createElement(Provider, { store, children: createElement(Probe) }));
	return value!;
}