// 本文件展示登录页和侧栏在深浅背景下的品牌预览。
import { pickBrandingSources } from "@/lib/hooks/useBranding";
import { Moon, PanelLeft, Sun } from "lucide-react";

interface Props {
	logo?: string;
	icon?: string;
}
/** BrandingPreview 使用草稿图片展示预览，不修改已保存的品牌配置。 */
export default function BrandingPreview({ logo = "", icon = "" }: Props) {
	return (
		<section className="space-y-4" aria-label="品牌效果预览">
			<div className="space-y-1">
				<h3 className="text-lg font-semibold tracking-tight">效果预览</h3>
				<p className="text-muted-foreground text-sm">请确认同一张图在两种背景上都清晰可见。图片按比例显示，保存前不会更改当前品牌。</p>
			</div>
			<div className="grid gap-4 md:grid-cols-2">
				{[false, true].map((dark) => {
					const { logoSrc, iconSrc } = pickBrandingSources(logo, icon, dark);
					// 预览固定使用 zinc 配色而不跟随主题变量，这样在任一主题下都能同时看到两种背景。
					const panel = dark ? "border-zinc-800 bg-zinc-950 text-zinc-100" : "border-zinc-200 bg-zinc-50 text-zinc-900";
					const surface = dark ? "border-zinc-800 bg-zinc-900" : "border-zinc-200 bg-white";
					const caption = dark ? "text-zinc-400" : "text-zinc-500";
					const ThemeIcon = dark ? Moon : Sun;
					// 尺寸与登录页、侧栏的真实展示保持一致：登录页 40px 高、展开侧栏 22px 高、折叠侧栏 22px 方。
					return (
						<div
							key={String(dark)}
							data-testid={`branding-preview-${dark ? "dark" : "light"}`}
							className={`space-y-4 rounded-sm border p-4 ${panel}`}
						>
							<div className="flex items-center justify-between">
								<h4 className="text-sm font-medium">{dark ? "深色背景" : "浅色背景"}</h4>
								<ThemeIcon className={`size-4 ${caption}`} strokeWidth={1.5} aria-hidden="true" />
							</div>
							<div className="space-y-1.5">
								<p className={`text-xs ${caption}`}>登录页</p>
								<div className={`flex h-24 items-center justify-center rounded-sm border ${surface}`}>
									<img src={logoSrc} alt="登录页 Logo 预览" className="max-h-[40px] w-auto max-w-[220px] object-contain" />
								</div>
							</div>
							<div className="grid grid-cols-[1fr_auto] gap-3">
								<div className="min-w-0 space-y-1.5">
									<p className={`text-xs ${caption}`}>展开侧栏</p>
									<div className={`flex h-12 items-center justify-between rounded-sm border px-3 ${surface}`}>
										<img src={logoSrc} alt="展开侧栏 Logo 预览" className="h-[22px] w-auto max-w-[150px] object-contain" />
										<PanelLeft className={`size-4 ${caption}`} strokeWidth={1.5} aria-hidden="true" />
									</div>
								</div>
								<div className="space-y-1.5">
									<p className={`text-xs ${caption}`}>折叠侧栏</p>
									<div className={`flex size-12 items-center justify-center rounded-sm border ${surface}`}>
										<img src={iconSrc} alt="折叠侧栏图标预览" className="size-[22px] object-contain" />
									</div>
								</div>
							</div>
						</div>
					);
				})}
			</div>
		</section>
	);
}