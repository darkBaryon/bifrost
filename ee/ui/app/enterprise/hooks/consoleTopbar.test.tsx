// 本文件验证基础信息解耦后真实 Topbar 仍按认证开关显示并执行原退出流程。
import type { ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";

const fixture = vi.hoisted(() => ({
	authEnabled: true,
	logout: vi.fn(),
	unwrap: vi.fn(),
	navigate: vi.fn(),
	onLogout: undefined as (() => Promise<void>) | undefined,
	Pass: ({ children }: { children?: ReactNode }) => children,
}));

vi.mock("@enterprise/hooks/useConsoleConfig", () => ({ useConsoleAuthEnabled: () => fixture.authEnabled }));
vi.mock("@/lib/store", () => ({ useGetVersionQuery: () => ({ data: "test" }), useLogoutMutation: () => [fixture.logout] }));
vi.mock("@/lib/contexts/topbarContext", () => ({
	useDescriptionSlotRef: () => undefined,
	useMobileFilterSlotRef: () => undefined,
	useTopbarTitle: () => undefined,
}));
vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => fixture.navigate,
	useLocation: ({ select }: { select: (location: { pathname: string }) => string }) => select({ pathname: "/workspace/routing-rules" }),
}));
vi.mock("@/components/notificationCenter", () => ({ default: () => null }));
vi.mock("@/components/themeToggle", () => ({ ThemeToggle: () => null }));
vi.mock("@/components/ui/sidebar", () => ({ SidebarTrigger: () => null }));
vi.mock("@/lib/hooks/useBranding", () => ({ useBranding: () => ({ logoSrc: "/logo.svg", logoAlt: "Bifrost" }) }));
vi.mock("next-themes", () => ({ useTheme: () => ({ resolvedTheme: "light" }) }));
vi.mock("@/components/ui/dropdownMenu", () => ({
	DropdownMenu: fixture.Pass,
	DropdownMenuContent: fixture.Pass,
	DropdownMenuGroup: fixture.Pass,
	DropdownMenuLabel: fixture.Pass,
	DropdownMenuTrigger: fixture.Pass,
	DropdownMenuSeparator: () => null,
	DropdownMenuItem: (props: { children: ReactNode; onClick?: () => Promise<void>; "data-testid"?: string }) => {
		if (props["data-testid"] === "topbar-logout-btn") fixture.onLogout = props.onClick;
		return <div data-testid={props["data-testid"]}>{props.children}</div>;
	},
}));

import Topbar from "@/components/topbar";

beforeEach(() => {
	fixture.authEnabled = true;
	fixture.logout.mockReset().mockReturnValue({ unwrap: fixture.unwrap });
	fixture.unwrap.mockReset().mockResolvedValue({ message: "Logout successful" });
	fixture.navigate.mockReset();
	fixture.onLogout = undefined;
});

describe("工作台退出入口", () => {
	it("启用认证时显示退出，调用原 mutation 并转到登录页", async () => {
		expect(renderToStaticMarkup(<Topbar />)).toContain('data-testid="topbar-logout-btn"');
		expect(fixture.onLogout).toBeTypeOf("function");
		await fixture.onLogout!();
		expect(fixture.logout).toHaveBeenCalledOnce();
		expect(fixture.unwrap).toHaveBeenCalledOnce();
		expect(fixture.navigate).toHaveBeenCalledWith({ to: "/login" });
	});

	it("服务端退出失败仍执行原登录页跳转", async () => {
		fixture.unwrap.mockRejectedValue(new Error("offline"));
		renderToStaticMarkup(<Topbar />);
		await expect(fixture.onLogout!()).rejects.toThrow("offline");
		expect(fixture.navigate).toHaveBeenCalledWith({ to: "/login" });
	});

	it("无认证且无账号资料时保持原隐藏行为", () => {
		fixture.authEnabled = false;
		expect(renderToStaticMarkup(<Topbar />)).not.toContain('data-testid="topbar-logout-btn"');
		expect(fixture.onLogout).toBeUndefined();
	});
});