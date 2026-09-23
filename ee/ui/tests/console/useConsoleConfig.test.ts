// 本文件验证基础信息端点的真实请求、缓存、刷新失败与 OSS/EE hook 合同。
import {
	useConsoleAuthEnabled as useOSSAuthEnabled,
	useConsoleConfigQuery as useOSSConfigQuery,
	CONSOLE_ONBOARDING_ENABLED as OSS_ONBOARDING,
} from "@/app/_fallbacks/enterprise/hooks/useConsoleConfig";
import { baseApi, getErrorMessage } from "@/lib/store/apis/baseApi";
import { configApi } from "@/lib/store/apis/configApi";
import { sessionApi } from "@/lib/store/apis/sessionApi";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/utils/port", () => ({ getApiBaseUrl: () => "http://console.test/api" }));

import { bootstrapEndpoint, coreConfigFixture, jsonResponse, renderHookValue, setupConsoleStore } from "./consoleTestHarness";
import { CONSOLE_ONBOARDING_ENABLED, useConsoleAuthEnabled, useConsoleConfigQuery } from "@enterprise/hooks/useConsoleConfig";

const bootstrapData = { is_db_connected: true, is_logs_connected: false, env_label: "EE", restart_required: { required: true } };

afterEach(() => vi.unstubAllGlobals());

describe("EE 基础信息查询", () => {
	it("使用独立 POST 端点、空对象和宿主 Cookie 凭据", async () => {
		const { store, requests } = setupConsoleStore(() => jsonResponse(bootstrapData));
		expect(await store.dispatch(bootstrapEndpoint.initiate()).unwrap()).toEqual(bootstrapData);
		expect(requests).toHaveLength(1);
		expect(requests[0].url).toBe("http://console.test/api/console/bootstrap");
		expect(requests[0].method).toBe("POST");
		expect(requests[0].credentials).toBe("include");
		expect(requests[0].headers.get("Content-Type")).toBe("application/json");
		expect(requests[0].headers.has("Authorization")).toBe(false);
		expect(await requests[0].json()).toEqual({});
	});

	it("基础信息与完整配置各自缓存，重复订阅不重发或相互覆盖", async () => {
		const { store, requests } = setupConsoleStore((request) =>
			jsonResponse(request.url.includes("/console/bootstrap") ? bootstrapData : coreConfigFixture),
		);
		await store.dispatch(bootstrapEndpoint.initiate()).unwrap();
		expect(configApi.endpoints.getCoreConfig.select({})(store.getState()).data).toBeUndefined();
		await store.dispatch(configApi.endpoints.getCoreConfig.initiate({})).unwrap();
		await store.dispatch(bootstrapEndpoint.initiate()).unwrap();
		await store.dispatch(configApi.endpoints.getCoreConfig.initiate({})).unwrap();
		expect(requests).toHaveLength(2);
		expect(bootstrapEndpoint.select()(store.getState()).data).toEqual(bootstrapData);
		expect(configApi.endpoints.getCoreConfig.select({})(store.getState()).data).toEqual(coreConfigFixture);
		expect(requests[1].url).toBe("http://console.test/api/config?from_db=false");
		expect(requests[1].method).toBe("GET");
	});

	it("设置保存的 Config 失效通知会刷新环境与重启状态", async () => {
		let refreshed = false;
		const updated = { ...bootstrapData, env_label: "Updated", restart_required: { required: false } };
		const { store, requests } = setupConsoleStore((request) => {
			if (request.method === "PUT") {
				refreshed = true;
				return jsonResponse(null);
			}
			return jsonResponse(refreshed ? updated : bootstrapData);
		});
		await store.dispatch(bootstrapEndpoint.initiate()).unwrap();
		await store.dispatch(configApi.endpoints.updateCoreConfig.initiate(coreConfigFixture)).unwrap();
		await vi.waitFor(() => expect(bootstrapEndpoint.select()(store.getState()).data).toEqual(updated));
		expect(requests.map((request) => request.method)).toEqual(["POST", "PUT", "POST"]);
	});

	it("首屏 503 没有可用数据，重试恢复后刷新失败仍保留成功缓存", async () => {
		let failed = true;
		const { store } = setupConsoleStore(() =>
			failed ? jsonResponse({ error: { message: "Service unavailable" } }, 503) : jsonResponse(bootstrapData),
		);
		const query = store.dispatch(bootstrapEndpoint.initiate());
		await query;
		expect(bootstrapEndpoint.select()(store.getState())).toMatchObject({ status: "rejected", error: { status: 503 } });
		expect(bootstrapEndpoint.select()(store.getState()).data).toBeUndefined();
		expect(getErrorMessage(bootstrapEndpoint.select()(store.getState()).error)).toBe("Service unavailable");
		failed = false;
		expect(await query.refetch().unwrap()).toEqual(bootstrapData);
		failed = true;
		await query.refetch();
		expect(bootstrapEndpoint.select()(store.getState())).toMatchObject({ status: "rejected", data: bootstrapData, error: { status: 503 } });
		const hook = renderHookValue(store, () => useConsoleConfigQuery());
		expect(hook.data).toEqual(bootstrapData);
		expect(hook.error).toMatchObject({ status: 503 });
	});

	it("skip 使已缓存的基础信息不参与当前 hook 状态", async () => {
		const { store, requests } = setupConsoleStore(() => jsonResponse(bootstrapData));
		await store.dispatch(bootstrapEndpoint.initiate()).unwrap();
		const skipped = renderHookValue(store, () => useConsoleConfigQuery({ skip: true }));
		expect(skipped.data).toBeUndefined();
		expect(skipped.isLoading).toBe(false);
		expect(skipped.isFetching).toBe(false);
		expect(renderHookValue(store, () => useConsoleConfigQuery({ skip: false })).data).toEqual(bootstrapData);
		expect(requests).toHaveLength(1);
	});

	// 服务器渲染不执行 effect，hook 不会自行发请求；“不自动请求完整配置”由浏览器冒烟验证。
	it("EE 退出开关来自会话状态；旧向导关闭", async () => {
		const { store } = setupConsoleStore(() => jsonResponse({ is_auth_enabled: true, has_valid_token: true }));
		expect(renderHookValue(store, useConsoleAuthEnabled)).toBe(false);
		await store.dispatch(sessionApi.endpoints.isAuthEnabled.initiate()).unwrap();
		expect(renderHookValue(store, useConsoleAuthEnabled)).toBe(true);
		expect(CONSOLE_ONBOARDING_ENABLED).toBe(false);
	});
});

describe("OSS 默认实现", () => {
	it("沿用完整配置、认证开关、重启原因及向导能力", async () => {
		const { store, requests } = setupConsoleStore(() => jsonResponse(coreConfigFixture));
		expect(renderHookValue(store, useOSSAuthEnabled)).toBe(false);
		await store.dispatch(configApi.endpoints.getCoreConfig.initiate({})).unwrap();
		expect(renderHookValue(store, () => useOSSConfigQuery()).data).toEqual(coreConfigFixture);
		expect(renderHookValue(store, useOSSAuthEnabled)).toBe(true);
		expect(renderHookValue(store, () => useOSSConfigQuery({ skip: true })).data).toBeUndefined();
		expect(requests.map((request) => request.url)).toEqual(["http://console.test/api/config?from_db=false"]);
		expect(OSS_ONBOARDING).toBe(true);
	});
});