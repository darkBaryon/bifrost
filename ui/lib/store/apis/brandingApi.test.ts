// 本文件验证品牌接口实际发出的 HTTP 方法、路径和保存请求体。
import { configureStore } from "@reduxjs/toolkit";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("./baseApi", async () => {
	const { createApi, fetchBaseQuery } = await import("@reduxjs/toolkit/query/react");
	return {
		baseApi: createApi({
			reducerPath: "brandingTest",
			baseQuery: fetchBaseQuery({ baseUrl: "http://branding.test/api" }),
			tagTypes: ["Branding"],
			endpoints: () => ({}),
		}),
	};
});

import { brandingApi } from "./brandingApi";

function setup() {
	const requests: Request[] = [];
	vi.stubGlobal("fetch", async (request: Request) => {
		requests.push(request);
		return new Response(JSON.stringify({ enabled: false, has_logo: false, has_icon: false }), {
			headers: { "Content-Type": "application/json" },
		});
	});
	const store = configureStore({
		reducer: { [brandingApi.reducerPath]: brandingApi.reducer },
		middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(brandingApi.middleware),
	});
	afterEach(() => store.dispatch(brandingApi.util.resetApiState()));
	return { requests, store };
}

afterEach(() => vi.unstubAllGlobals());

describe("品牌接口请求", () => {
	it("通过 POST get 读取设置", async () => {
		const { requests, store } = setup();
		const query = store.dispatch(brandingApi.endpoints.getBranding.initiate());
		await query.unwrap();
		query.unsubscribe();
		expect(requests).toHaveLength(1);
		expect(requests[0].method).toBe("POST");
		expect(requests[0].url).toBe("http://branding.test/api/branding/get");
	});

	it("通过 POST update 保存并保留请求字段", async () => {
		const { requests, store } = setup();
		const payload = { logo: "data:image/png;base64,example", icon: "" };
		await store.dispatch(brandingApi.endpoints.updateBranding.initiate(payload)).unwrap();
		expect(requests).toHaveLength(1);
		expect(requests[0].method).toBe("POST");
		expect(requests[0].url).toBe("http://branding.test/api/branding/update");
		expect(await requests[0].json()).toEqual(payload);
	});

	it("通过 POST reset 恢复默认", async () => {
		const { requests, store } = setup();
		await store.dispatch(brandingApi.endpoints.resetBranding.initiate()).unwrap();
		expect(requests).toHaveLength(1);
		expect(requests[0].method).toBe("POST");
		expect(requests[0].url).toBe("http://branding.test/api/branding/reset");
	});
});