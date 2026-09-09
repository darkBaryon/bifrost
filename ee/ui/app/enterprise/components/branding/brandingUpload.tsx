// 本文件渲染一个图片位置的缩略图、上传、移除和字段错误。
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { BRANDING_MAX_EDGE_PX, BRANDING_MAX_MIB, BRANDING_MAX_PIXELS } from "@enterprise/lib/schemas/branding";
import { Image as ImagePlaceholder, Upload, X } from "lucide-react";
import { useRef } from "react";

interface Props {
	slot: "logo" | "icon";
	disabled: boolean;
	hasImage: boolean;
	/** 当前生效的图片（草稿优先于已保存），只用于缩略图；空串表示没有图片。 */
	src?: string;
	fileName?: string;
	error?: string;
	onSelect: (file: File) => void;
	onRemove: () => void;
}
/** BrandingUpload 展示图片选择控件，通过回调交给表单处理，不直接保存。 */
export default function BrandingUpload({ slot, disabled, hasImage, src, fileName, error, onSelect, onRemove }: Props) {
	const input = useRef<HTMLInputElement>(null);
	const title = slot === "logo" ? "Logo" : "小图标（选填）";
	const status = fileName || (hasImage ? "已设置图片" : "未选择图片");
	return (
		<div className="space-y-3 rounded-sm border p-4">
			<div className="flex gap-4">
				{/* 缩略图只反映本位置自己的图片；小图标为空时的沿用规则由预览区体现。 */}
				<div className="bg-muted/40 flex size-16 shrink-0 items-center justify-center overflow-hidden rounded-sm border" aria-hidden="true">
					{src ? (
						<img src={src} alt="" className="max-h-12 max-w-12 object-contain" />
					) : (
						<ImagePlaceholder className="text-muted-foreground size-6" strokeWidth={1.5} />
					)}
				</div>
				<div className="min-w-0 flex-1 space-y-3">
					<div className="space-y-1">
						<Label htmlFor={`branding-${slot}`}>{title}</Label>
						<p className="text-muted-foreground text-sm">
							{slot === "logo" ? "用于登录页和展开侧栏，浅色与深色主题共用。" : "用于折叠侧栏；留空时按比例沿用 Logo。"}
						</p>
					</div>
					<input
						ref={input}
						hidden
						id={`branding-${slot}`}
						type="file"
						accept="image/png,image/jpeg"
						disabled={disabled}
						data-testid={`branding-upload-${slot}`}
						aria-describedby={error ? `branding-error-${slot}` : undefined}
						onChange={(event) => {
							const file = event.target.files?.[0];
							// 清空原生输入以便重复选择同一文件；可见状态由表单草稿提供。
							event.target.value = "";
							if (file) onSelect(file);
						}}
					/>
					<div className="flex flex-wrap items-center gap-2">
						<Button
							type="button"
							variant="outline"
							size="sm"
							disabled={disabled}
							onClick={() => input.current?.click()}
							data-testid={`branding-select-${slot}`}
							aria-label={`选择${title}图片`}
							aria-describedby={error ? `branding-error-${slot}` : undefined}
						>
							<Upload />
							{hasImage ? "更换图片" : "选择图片"}
						</Button>
						{/* 文件名用前景色并只截断尾部，完整名字放在 title 里，避免长文件名撑破布局又看不清。 */}
						<span
							className={`bg-muted/50 inline-flex h-8 max-w-64 min-w-0 items-center rounded-sm border px-2.5 text-sm ${fileName ? "text-foreground" : "text-muted-foreground"}`}
							role="status"
							title={fileName}
							data-testid={`branding-file-${slot}`}
						>
							<span className="truncate">{status}</span>
						</span>
						<Button
							type="button"
							variant="ghost"
							size="sm"
							className="text-muted-foreground hover:text-destructive"
							disabled={disabled || (!hasImage && !error)}
							data-testid={`branding-remove-${slot}`}
							onClick={onRemove}
						>
							<X />
							移除{slot === "logo" ? " Logo" : "小图标"}
						</Button>
					</div>
				</div>
			</div>
			{/* 限制说明放整卡宽度，避免在缩略图右侧的窄列里折成两行。 */}
			<p className="text-muted-foreground text-xs">
				PNG / JPEG，每张 ≤ {BRANDING_MAX_MIB} MiB，单边 ≤ {BRANDING_MAX_EDGE_PX} 像素，总像素 ≤ {BRANDING_MAX_PIXELS / 10000} 万。
			</p>
			{error && (
				<p role="alert" id={`branding-error-${slot}`} data-testid={`branding-error-${slot}`} className="text-destructive text-sm">
					{error}
				</p>
			)}
		</div>
	);
}