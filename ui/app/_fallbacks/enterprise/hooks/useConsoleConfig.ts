import { useGetCoreConfigQuery } from "@/lib/store/apis/configApi";
import type { ConsoleConfigQueryOptions, ConsoleConfigQueryResult } from "@/lib/types/console";

// OSS keeps the existing full-config query and shared RTK Query cache.
export function useConsoleConfigQuery(options?: ConsoleConfigQueryOptions): ConsoleConfigQueryResult {
	return useGetCoreConfigQuery({}, options);
}

// The OSS sign-out entry follows its existing dashboard authentication setting.
export function useConsoleAuthEnabled(): boolean {
	const { data } = useGetCoreConfigQuery({});
	return data?.auth_config?.is_enabled || false;
}

export const CONSOLE_ONBOARDING_ENABLED = true;