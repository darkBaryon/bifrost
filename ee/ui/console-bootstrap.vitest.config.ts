// 本配置独立验证外壳基础查询，不修改上游全局测试配置，也不要求先生成 EE 覆盖层。
import react from "@vitejs/plugin-react";
import path from "node:path";
import { defineConfig } from "vitest/config";

const uiRoot = path.resolve(__dirname, "../../ui");
const fallbackRoot = path.join(uiRoot, "app/_fallbacks/enterprise");

export default defineConfig({
	root: uiRoot,
	plugins: [react()],
	resolve: {
		// 与生产构建一致：临时副本可复用现有 node_modules 符号链接。
		preserveSymlinks: true,
		dedupe: [
			"react",
			"react-dom",
			"react-redux",
			"@reduxjs/toolkit",
			"@tanstack/react-router",
			"react-cookie",
			"nuqs",
			"sonner",
			"next-themes",
		],
		alias: [
			{ find: "@enterprise/hooks/useConsoleConfig", replacement: path.resolve(__dirname, "app/enterprise/hooks/useConsoleConfig.ts") },
			{ find: "@enterprise", replacement: fallbackRoot },
			{ find: "@schemas", replacement: path.join(fallbackRoot, "lib/schemas") },
			{ find: "@", replacement: uiRoot },
		],
	},
	define: { "process.env.BIFROST_IS_ENTERPRISE": JSON.stringify("true") },
	test: {
		include: ["../ee/ui/app/enterprise/hooks/*.test.{ts,tsx}"],
		environment: "node",
	},
});