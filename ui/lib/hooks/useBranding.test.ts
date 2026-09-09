import { beforeEach, describe, expect, it, vi } from "vitest";
import type { BrandingState } from "@/lib/store/apis/brandingApi";
const state = vi.hoisted(() => ({ enterprise: true, branding: undefined as BrandingState | undefined }));
vi.mock("@/lib/constants/config", () => ({
	get IS_ENTERPRISE() {
		return state.enterprise;
	},
}));
vi.mock("@/lib/store/apis/brandingApi", () => ({
	useGetBrandingQuery: (_: unknown, options: { skip: boolean }) => ({ data: options.skip ? undefined : state.branding }),
}));
vi.mock("@/lib/utils/port", () => ({ getApiBaseUrl: () => "/api" }));
// The hook is called outside a renderer, so `react` is replaced wholesale with
// an eager useEffect. If useBranding ever imports another React API, extend
// this mock (there is no @testing-library/react in ui/package.json).
vi.mock("react", () => ({ useEffect: (effect: () => void) => effect() }));

beforeEach(() => {
	vi.resetModules();
	state.enterprise = true;
	state.branding = undefined;
	const cache = new Map<string, string>();
	vi.stubGlobal("localStorage", {
		getItem: (key: string) => cache.get(key) ?? null,
		setItem: (key: string, value: string) => cache.set(key, value),
		removeItem: (key: string) => cache.delete(key),
	});
});
describe("branding assets", () => {
	it.each([false, true])("uses one logo for both surfaces and theme dark=%s", async (dark) => {
		state.branding = { enabled: true, has_logo: true, has_icon: false, logo_url: "/api/branding/assets/logo/hash" };
		const { useBranding } = await import("./useBranding");
		expect(useBranding(dark)).toMatchObject({ logoSrc: state.branding.logo_url, iconSrc: state.branding.logo_url, isCustom: true });
	});
	it("prefers an independent icon and supports icon only", async () => {
		state.branding = { enabled: true, has_logo: true, has_icon: true, logo_url: "/logo", icon_url: "/icon" };
		const { useBranding } = await import("./useBranding");
		expect(useBranding(false).iconSrc).toBe("/icon");
		state.branding = { enabled: true, has_logo: false, has_icon: true, icon_url: "/icon" };
		expect(useBranding(true)).toMatchObject({ logoSrc: "/bifrost-logo-dark.webp", iconSrc: "/icon" });
	});
	it("reset clears cached overrides and restores theme defaults", async () => {
		state.branding = { enabled: true, has_logo: true, has_icon: false, logo_url: "/logo" };
		const { useBranding, getCachedBrandingAssets } = await import("./useBranding");
		useBranding(false);
		expect(getCachedBrandingAssets(false).iconSrc).toBe("/logo");
		state.branding = { enabled: false, has_logo: false, has_icon: false };
		expect(useBranding(true)).toMatchObject({ logoSrc: "/bifrost-logo-dark.webp", iconSrc: "/bifrost-icon-dark.webp", isCustom: false });
		expect(localStorage.getItem("bifrost-branding")).toBeNull();
	});
	it("OSS ignores leftover enterprise cache", async () => {
		localStorage.setItem(
			"bifrost-branding",
			JSON.stringify({ enabled: true, has_logo: true, has_icon: true, logo_url: "/logo", icon_url: "/icon" }),
		);
		state.enterprise = false;
		const { useBranding, getCachedBrandingAssets } = await import("./useBranding");
		expect(useBranding(false)).toMatchObject({ logoSrc: "/bifrost-logo.webp", iconSrc: "/bifrost-icon.webp" });
		expect(getCachedBrandingAssets(true).iconSrc).toBe("/bifrost-icon-dark.webp");
	});
});