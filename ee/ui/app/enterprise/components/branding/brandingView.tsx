// 本文件组合品牌设置页面；branding 目录负责品牌编辑与预览，不负责登录页品牌展示或数据库存取。
import PageTitle from "@/components/pageTitle";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@/components/ui/alertDialog";
import { CircleAlert, CircleCheck } from "lucide-react";
import BrandingPreview from "./brandingPreview";
import BrandingUpload from "./brandingUpload";
import { useBrandingForm } from "./useBrandingForm";

/** BrandingView 渲染品牌设置表单、预览和恢复默认确认框。 */
export default function BrandingView() {
	const {
		data,
		isLoading,
		isError,
		isFetching,
		isDirty,
		busy,
		disabled,
		saveDisabled,
		logo,
		icon,
		fileNames,
		fileErrors,
		error,
		notice,
		confirmReset,
		setConfirmReset,
		reading,
		saving,
		reload,
		select,
		save,
		restore,
		handleSubmit,
		remove,
	} = useBrandingForm();
	if (isLoading)
		return (
			<p className="text-muted-foreground py-4 text-sm" data-testid="branding-loading">
				正在读取品牌设置…
			</p>
		);
	return (
		<div className="mx-auto w-full max-w-4xl space-y-6 py-4" data-testid="branding-settings">
			{/* 标题与其他设置页一样提升到顶栏；页面内只保留分节标题。 */}
			<PageTitle title="品牌定制">上传自己的 Logo 与小图标，替换登录页和侧栏的默认品牌。先预览，满意后再保存。</PageTitle>
			{(isError || error) && (
				<Alert variant="destructive">
					<CircleAlert />
					<AlertDescription className="flex w-full flex-wrap items-center justify-between gap-2">
						<span>{error || "无法读取品牌配置，请重试。"}</span>
						<Button type="button" variant="outline" size="sm" data-testid="branding-retry" onClick={reload} disabled={busy || isFetching}>
							重新读取
						</Button>
					</AlertDescription>
				</Alert>
			)}
			{notice && (
				<Alert role="status" data-testid="branding-success">
					<CircleCheck className="text-primary" />
					<AlertDescription>{notice}</AlertDescription>
				</Alert>
			)}
			<form onSubmit={handleSubmit(save)} className="space-y-6">
				<section className="space-y-4" aria-label="品牌图片">
					<div className="space-y-1">
						<h3 className="text-lg font-semibold tracking-tight">品牌图片</h3>
						<p className="text-muted-foreground text-sm">上传自己的品牌图片，先预览，满意后再保存。</p>
					</div>
					<div className="grid gap-4 md:grid-cols-2">
						{(["logo", "icon"] as const).map((slot) => {
							const src = slot === "logo" ? logo : icon;
							return (
								<BrandingUpload
									key={slot}
									slot={slot}
									disabled={disabled}
									hasImage={Boolean(src)}
									src={src}
									fileName={fileNames[slot]}
									error={fileErrors[slot]}
									onSelect={(file) => select(slot, file)}
									onRemove={() => remove(slot)}
								/>
							);
						})}
					</div>
				</section>
				<BrandingPreview logo={logo} icon={icon} />
				<div className="flex flex-wrap items-center gap-3 border-t pt-4">
					<Button type="submit" disabled={saveDisabled || !isDirty} data-testid="branding-save">
						{saving ? "正在保存…" : "保存"}
					</Button>
					<Button
						type="button"
						variant="outline"
						disabled={disabled || !data?.enabled}
						data-testid="branding-reset"
						onClick={() => setConfirmReset(true)}
					>
						恢复默认
					</Button>
					<span className="text-muted-foreground text-sm">{reading ? "正在读取图片…" : isDirty ? "有未保存的更改" : "当前设置已保存"}</span>
				</div>
			</form>
			<AlertDialog open={confirmReset} onOpenChange={setConfirmReset}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>恢复默认品牌图片？</AlertDialogTitle>
						<AlertDialogDescription>将清除已保存的 Logo 和小图标，并丢弃当前未保存的选择。此操作立即生效。</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel data-testid="branding-reset-cancel">取消</AlertDialogCancel>
						<AlertDialogAction data-testid="branding-reset-confirm" onClick={restore} disabled={busy}>
							恢复默认
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</div>
	);
}