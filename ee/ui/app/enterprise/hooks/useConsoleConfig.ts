// 本文件将公共外壳接到个人会话可读取的基础信息，完整设置仍由原接口授权。
import { baseApi } from "@/lib/store/apis/baseApi";
import { useIsAuthEnabledQuery } from "@/lib/store/apis/sessionApi";
import type { ConsoleConfig, ConsoleConfigQueryOptions, ConsoleConfigQueryResult } from "@/lib/types/console";

const consoleApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getConsoleBootstrap: builder.query<ConsoleConfig, void>({
			query: () => ({
				url: "/console/bootstrap",
				method: "POST",
				body: {},
			}),
			// 复用设置保存的失效通知，独立端点不会覆盖完整配置缓存。
			providesTags: ["Config"],
		}),
	}),
});

// useConsoleConfigQuery 保留跳过查询、首次失败和已有缓存刷新失败的原生查询状态。
export function useConsoleConfigQuery(options?: ConsoleConfigQueryOptions): ConsoleConfigQueryResult {
	return consoleApi.useGetConsoleBootstrapQuery(undefined, options);
}

// useConsoleAuthEnabled 复用会话探测，使没有设置权限的账号仍可退出。
export function useConsoleAuthEnabled(): boolean {
	const { data } = useIsAuthEnabledQuery();
	return data?.is_auth_enabled || false;
}

// CONSOLE_ONBOARDING_ENABLED 关闭依赖上游共享管理员/SSO 配置的旧向导。
export const CONSOLE_ONBOARDING_ENABLED = false;