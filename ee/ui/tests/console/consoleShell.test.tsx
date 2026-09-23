// 本文件锁定实际 ClientLayout 的渲染边界；网络行为由真实 RTK 测试与浏览器冒烟覆盖。
import type { ConsoleConfigQueryOptions, ConsoleConfigQueryResult } from "@/lib/types/console";
import type { ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixture = vi.hoisted(() => ({
	query: {} as ConsoleConfigQueryResult,
	options: undefined as ConsoleConfigQueryOptions | undefined,
	pathname: "/workspace/routing-rules",
	matches: [] as { staticData: { publicShell?: boolean; tempTokenScoped?: boolean } }[],
	authLoading: false,
	auth: { is_auth_enabled: true, has_valid_token: true },
	retry: undefined as (() => void) | undefined,
	Pass: ({ children }: { children?: ReactNode }) => children,
}));

vi.mock("@enterprise/hooks/useConsoleConfig", () => ({
	CONSOLE_ONBOARDING_ENABLED: false,
	useConsoleConfigQuery: (options?: ConsoleConfigQueryOptions) => {
		fixture.options = options;
		return fixture.query;
	},
}));
vi.mock("@tanstack/react-router", () => ({
	useMatches: () => fixture.matches,
	useLocation: ({ select }: { select: (location: { pathname: string }) => string }) => select({ pathname: fixture.pathname }),
}));
vi.mock("@/lib/store", () => ({
	ReduxProvider: fixture.Pass,
	getErrorMessage: () => "Service unavailable",
	useIsAuthEnabledQuery: () => ({ data: fixture.auth, isLoading: fixture.authLoading }),
}));
vi.mock("@enterprise/lib/contexts/rbacContext", () => ({ RbacProvider: fixture.Pass, useRbacContext: () => ({ isLoading: false }) }));
vi.mock("@/components/progressBar", () => ({ default: fixture.Pass }));
vi.mock("@/components/themeProvider", () => ({ ThemeProvider: fixture.Pass }));
vi.mock("@/components/ui/sidebar", () => ({ SidebarProvider: fixture.Pass }));
vi.mock("@/lib/contexts/topbarContext", () => ({ TopbarProvider: fixture.Pass }));
vi.mock("@/hooks/useWebSocket", () => ({ WebSocketProvider: fixture.Pass }));
vi.mock("react-cookie", () => ({ CookiesProvider: fixture.Pass }));
vi.mock("nuqs/adapters/tanstack-router", () => ({ NuqsAdapter: fixture.Pass }));
vi.mock("@/hooks/useStoreSync", () => ({ useStoreSync: () => {} }));
vi.mock("@/hooks/useNotificationSync", () => ({ useNotificationSync: () => {} }));
vi.mock("sonner", () => ({ toast: { error: vi.fn() }, Toaster: () => null }));
vi.mock("@/components/sidebar", () => ({ default: () => <aside data-testid="sidebar" /> }));
vi.mock("@/components/topbar", () => ({ default: () => <header data-testid="topbar" /> }));
vi.mock("@/components/trialExpiryBanner", () => ({ default: () => null }));
vi.mock("@/components/fullPageLoader", () => ({ default: () => <div data-testid="loading" /> }));
vi.mock("@/components/notAvailableBanner", () => ({ default: () => <div data-testid="unavailable" /> }));
vi.mock("@/components/onboardingWidget", () => ({ default: () => <div data-testid="old-onboarding" /> }));
vi.mock("@/components/ui/button", () => ({
	Button: ({ children, onClick, disabled }: { children: ReactNode; onClick: () => void; disabled: boolean }) => {
		fixture.retry = onClick;
		return (
			<button data-testid="retry" disabled={disabled}>
				{children}
			</button>
		);
	},
}));

import { ClientLayout } from "@/app/clientLayout";

const ready = { is_db_connected: true, is_logs_connected: true, env_label: null };
const renderShell = () =>
	renderToStaticMarkup(
		<ClientLayout>
			<div data-testid="business-content" />
		</ClientLayout>,
	);

beforeEach(() => {
	fixture.query = { data: undefined, error: undefined, isLoading: false, isFetching: false, refetch: vi.fn() };
	fixture.options = undefined;
	fixture.pathname = "/workspace/routing-rules";
	fixture.matches = [];
	fixture.authLoading = false;
	fixture.auth = { is_auth_enabled: true, has_valid_token: true };
	fixture.retry = undefined;
});
afterEach(() => vi.unstubAllGlobals());

describe("工作台基础信息渲染", () => {
	it("首次等待显示加载器，不提前呈现业务正文", () => {
		fixture.query.isLoading = true;
		const html = renderShell();
		expect(html).toContain('data-testid="loading"');
		expect(html).not.toContain('data-testid="business-content"');
	});

	it("首次失败显示原失败视图与重试入口，重试恢复后呈现正文", () => {
		fixture.query.error = { status: 503 };
		expect(renderShell()).toContain('data-testid="config-unreachable"');
		fixture.retry?.();
		expect(fixture.query.refetch).toHaveBeenCalledOnce();
		fixture.query.isFetching = true;
		expect(renderShell()).toContain('data-testid="retry" disabled=""');
		fixture.query = { ...fixture.query, data: ready, error: undefined, isFetching: false };
		expect(renderShell()).toContain('data-testid="business-content"');
	});

	it("已有成功缓存后刷新失败保留正文和 EE 的无旧向导状态", () => {
		fixture.query.data = ready;
		fixture.query.error = { status: 503 };
		const html = renderShell();
		expect(html).toContain('data-testid="business-content"');
		expect(html).not.toContain('data-testid="config-unreachable"');
		expect(html).not.toContain('data-testid="old-onboarding"');
	});

	it("配置库不可用时显示原不可用视图", () => {
		fixture.query.data = { ...ready, is_db_connected: false, is_logs_connected: false };
		expect(renderShell()).toContain('data-testid="unavailable"');
	});

	it("只有日志库时，仅日志路径可以显示正文", () => {
		fixture.query.data = { ...ready, is_db_connected: false };
		expect(renderShell()).toContain('data-testid="unavailable"');
		fixture.pathname = "/workspace/logs/requests";
		expect(renderShell()).toContain('data-testid="business-content"');
	});
});

describe("无需完整外壳的路由", () => {
	it("公共页面跳过基础查询与外壳挂载", () => {
		fixture.matches = [{ staticData: { publicShell: true } }];
		const html = renderShell();
		expect(fixture.options?.skip).toBe(true);
		expect(html).toContain('data-testid="business-content"');
		expect(html).not.toContain('data-testid="sidebar"');
	});

	it("临时页面认证探测未完成时跳过基础查询", () => {
		fixture.matches = [{ staticData: { tempTokenScoped: true } }];
		fixture.authLoading = true;
		expect(renderShell()).toContain('data-testid="loading"');
		expect(fixture.options?.skip).toBe(true);
	});

	it("匿名临时令牌访问保持最小外壳，已登录访问仍查询基础状态", () => {
		fixture.matches = [{ staticData: { tempTokenScoped: true } }];
		fixture.auth.has_valid_token = false;
		vi.stubGlobal("window", { location: { hash: "#t=test-token" } });
		expect(renderShell()).not.toContain('data-testid="sidebar"');
		expect(fixture.options?.skip).toBe(true);
		fixture.auth.has_valid_token = true;
		renderShell();
		expect(fixture.options?.skip).toBe(false);
	});
});