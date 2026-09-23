// 本文件验证实际 Sidebar 的基础状态提示与旧向导停用入口，不改动导航权限判断。
import type { ConsoleConfig } from "@/lib/types/console";
import type { ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";

const fixture = vi.hoisted(() => ({
	config: {} as ConsoleConfig,
	onboardingEnabled: false,
	checklistSkip: undefined as boolean | undefined,
	Pass: ({ children }: { children?: ReactNode }) => children,
}));

vi.mock("@enterprise/hooks/useConsoleConfig", () => ({
	get CONSOLE_ONBOARDING_ENABLED() {
		return fixture.onboardingEnabled;
	},
	useConsoleConfigQuery: () => ({ data: fixture.config }),
}));
vi.mock("@/hooks/useOnboardingChecklist", () => ({
	HIDDEN_UNTIL_NAV_COOKIE: "hidden",
	REMIND_LATER_COOKIE: "later",
	useOnboardingChecklist: ({ skip }: { skip: boolean }) => {
		fixture.checklistSkip = skip;
		// 即使之前有缓存，EE 能力开关也必须禁止恢复入口。
		return { steps: [{ id: "auth", complete: false }], skippedIds: [], checklistReady: true, isDismissedForAll: false };
	},
}));
vi.mock("@/lib/store", () => ({ useGetVersionQuery: () => ({}), useGetLatestReleaseQuery: () => ({}) }));
vi.mock("@enterprise/lib", () => ({ RbacResource: {}, RbacOperation: {}, useRbac: () => false }));
vi.mock("@enterprise/components/branding/poweredByBifrost", () => ({ default: () => null }));
vi.mock("@/lib/hooks/useBranding", () => ({ useBranding: () => ({ logoSrc: "/logo.svg", iconSrc: "/icon.svg", logoAlt: "Bifrost" }) }));
vi.mock("@/hooks/useWebSocket", () => ({ useWebSocket: () => ({ isConnected: false }) }));
vi.mock("next-themes", () => ({ useTheme: () => ({ resolvedTheme: "light" }) }));
vi.mock("react-cookie", () => ({ useCookies: () => [{ hidden: true }, vi.fn(), vi.fn()] }));
vi.mock("@tanstack/react-router", () => ({
	Link: fixture.Pass,
	useNavigate: () => vi.fn(),
	useLocation: ({ select }: { select: (location: { pathname: string; searchStr: string }) => string }) =>
		select({ pathname: "/workspace/routing-rules", searchStr: "" }),
}));
vi.mock("@/components/ui/sidebar", () => ({
	Sidebar: fixture.Pass,
	SidebarContent: fixture.Pass,
	SidebarGroup: fixture.Pass,
	SidebarGroupContent: fixture.Pass,
	SidebarHeader: fixture.Pass,
	// 导航子项不属于本案验证范围，保留 Sidebar 自身的数据消费与提示渲染。
	SidebarMenu: () => null,
	SidebarMenuButton: fixture.Pass,
	SidebarMenuItem: fixture.Pass,
	SidebarMenuSub: fixture.Pass,
	SidebarMenuSubButton: fixture.Pass,
	SidebarMenuSubItem: fixture.Pass,
	useSidebar: () => ({ state: "expanded", isMobile: false, toggleSidebar: vi.fn() }),
}));
vi.mock("@/components/ui/tooltip", () => ({ Tooltip: fixture.Pass, TooltipContent: fixture.Pass, TooltipTrigger: fixture.Pass }));
vi.mock("@/components/ui/promoCardStack", () => ({
	PromoCardStack: ({ cards }: { cards: { id: string; title: string; description: ReactNode }[] }) => (
		<>
			{cards.map((card) => (
				<section key={card.id} data-testid={card.id}>
					{card.title}
					{card.description}
				</section>
			))}
		</>
	),
}));

import Sidebar from "@/components/sidebar";

beforeEach(() => {
	fixture.config = { is_db_connected: true, is_logs_connected: true, env_label: "TEST ENV", restart_required: { required: true } };
	fixture.onboardingEnabled = false;
	fixture.checklistSkip = undefined;
});

describe("侧栏基础信息与向导能力", () => {
	it("EE 跳过旧向导查询，即使已有缓存与隐藏 Cookie 也不出现恢复入口", () => {
		const html = renderToStaticMarkup(<Sidebar />);
		expect(fixture.checklistSkip).toBe(true);
		expect(html).not.toContain('data-testid="onboarding-incomplete"');
	});

	it("保留环境标识与不含内部原因的通用重启提示", () => {
		const html = renderToStaticMarkup(<Sidebar />);
		expect(html).toContain("TEST ENV");
		expect(html).toContain('data-testid="restart-required"');
		expect(html).toContain("Configuration changes require a server restart to take effect.");
	});

	it("重启状态缺省或为 false 时不显示重启提示", () => {
		fixture.config.restart_required = undefined;
		expect(renderToStaticMarkup(<Sidebar />)).not.toContain('data-testid="restart-required"');
		fixture.config.restart_required = { required: false };
		expect(renderToStaticMarkup(<Sidebar />)).not.toContain('data-testid="restart-required"');
	});

	it("开启旧向导能力时保留原查询、恢复入口和配置重启原因", () => {
		fixture.onboardingEnabled = true;
		fixture.config.restart_required = { required: true, reason: "OSS reason" };
		const html = renderToStaticMarkup(<Sidebar />);
		expect(fixture.checklistSkip).toBe(false);
		expect(html).toContain('data-testid="onboarding-incomplete"');
		expect(html).toContain("OSS reason");
		fixture.config.is_db_connected = false;
		renderToStaticMarkup(<Sidebar />);
		expect(fixture.checklistSkip).toBe(true);
	});
});