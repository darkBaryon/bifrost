// 本文件管理品牌表单草稿、读图和保存操作，不负责渲染界面。
import { resolveBrandingAssetUrl } from "@/lib/hooks/useBranding";
import { getErrorMessage } from "@/lib/store/apis/baseApi";
import {
	brandingApi,
	useGetBrandingQuery,
	useResetBrandingMutation,
	useUpdateBrandingMutation,
	type BrandingPayload,
} from "@/lib/store/apis/brandingApi";
import { brandingFileSchema, brandingFormSchema, type BrandingForm } from "@enterprise/lib/schemas/branding";
import { zodResolver } from "@hookform/resolvers/zod";
import { useEffect, useRef, useState } from "react";
import { useForm } from "react-hook-form";
import { useDispatch } from "react-redux";
import type { AppDispatch } from "@/lib/store";
import { readImage, toUploadData } from "./image";

function failureMessage(error: unknown): string {
	const status = error && typeof error === "object" && "status" in error ? error.status : undefined;
	// 同时提交两张图片时，保留服务端指出的出错位置，方便用户定位。
	const detail = getErrorMessage(error);
	if (status === 413) return `图片或请求过大，请换一张较小的图片后重试。（${detail}）`;
	if (status === 400) return `图片未通过校验，请使用完整的 PNG / JPEG，并检查格式、大小和尺寸。（${detail}）`;
	if (status === 401 || status === 403) return "管理员登录已失效或无权操作，请重新登录。";
	return "操作失败，未保存的选择已保留。请先重新读取当前配置，确认结果后重试。";
}

/** useBrandingForm 提供草稿和表单操作；保存成功才清空草稿，失败时保留选择供核对和重试。 */
export function useBrandingForm() {
	const { data, isLoading, isError, isFetching, refetch } = useGetBrandingQuery();
	const [update, { isLoading: saving }] = useUpdateBrandingMutation();
	const [resetBranding, { isLoading: resetting }] = useResetBrandingMutation();
	const dispatch = useDispatch<AppDispatch>();
	const {
		watch,
		setValue,
		reset,
		handleSubmit,
		formState: { isDirty },
	} = useForm<BrandingForm>({ resolver: zodResolver(brandingFormSchema), defaultValues: { logo: undefined, icon: undefined } });
	const draft = watch();
	const [fileNames, setFileNames] = useState<Partial<Record<"logo" | "icon", string>>>({});
	const [reading, setReading] = useState(false);
	const [fileErrors, setFileErrors] = useState<Partial<Record<"logo" | "icon", string>>>({});
	const [error, setError] = useState("");
	const [notice, setNotice] = useState("");
	const [confirmReset, setConfirmReset] = useState(false);
	const [needsRefresh, setNeedsRefresh] = useState(false);
	// 选择变化或组件卸载后，旧的读图结果不能覆盖当前草稿。
	const generation = useRef(0);
	useEffect(
		() => () => {
			generation.current++;
		},
		[],
	);
	// 保存或重置后已使用返回值更新缓存；后台重读不禁用表单，避免按钮闪烁。
	const busy = reading || saving || resetting;
	const disabled = busy || isError || !data || needsRefresh;
	// 图片错误只阻止提交，仍允许重新选图或移除，避免用户无法纠正错误。
	const saveDisabled = disabled || Boolean(fileErrors.logo || fileErrors.icon);
	const savedLogo = data?.has_logo ? resolveBrandingAssetUrl(data.logo_url) : "";
	const savedIcon = data?.has_icon ? resolveBrandingAssetUrl(data.icon_url) : "";
	const logo = draft.logo ?? savedLogo;
	const icon = draft.icon ?? savedIcon;
	async function select(slot: "logo" | "icon", file: File) {
		const seq = ++generation.current;
		setNotice("");
		const result = brandingFileSchema.safeParse(file);
		if (!result.success) {
			setFileErrors((prev) => ({ ...prev, [slot]: result.error.issues[0].message }));
			return;
		}
		setReading(true);
		try {
			const image = await readImage(file);
			if (seq !== generation.current) return;
			setValue(slot, image, { shouldDirty: true });
			setFileNames((prev) => ({ ...prev, [slot]: file.name }));
			setFileErrors((prev) => ({ ...prev, [slot]: undefined }));
		} catch (error) {
			if (seq === generation.current)
				setFileErrors((prev) => ({ ...prev, [slot]: error instanceof Error ? error.message : "无法读取图片" }));
		} finally {
			if (seq === generation.current) setReading(false);
		}
	}
	async function save(values: BrandingForm) {
		// 表单提交也校验，避免通过回车等入口绕过按钮的禁用状态。
		if (saveDisabled) return;
		setError("");
		setNotice("");
		const payload: BrandingPayload = {};
		if (values.logo !== undefined) payload.logo = toUploadData(values.logo);
		if (values.icon !== undefined) payload.icon = toUploadData(values.icon);
		try {
			const state = await update(payload).unwrap();
			dispatch(brandingApi.util.updateQueryData("getBranding", undefined, () => state));
			reset();
			setFileNames({});
			setFileErrors({});
			setNotice("品牌设置已保存。其他页面刷新后也会显示新图片。");
		} catch (error) {
			setError(failureMessage(error));
			setNeedsRefresh(true);
		}
	}
	async function restore() {
		setConfirmReset(false);
		setError("");
		setNotice("");
		try {
			const state = await resetBranding().unwrap();
			dispatch(brandingApi.util.updateQueryData("getBranding", undefined, () => state));
			reset();
			setFileNames({});
			setFileErrors({});
			setNotice("已恢复默认品牌图片。");
		} catch (error) {
			setError(failureMessage(error));
			setNeedsRefresh(true);
		}
	}
	async function reload() {
		const result = await refetch();
		if (!result.error) {
			setNeedsRefresh(false);
			setError("");
		}
	}
	function remove(slot: "logo" | "icon") {
		generation.current++;
		setValue(slot, "", { shouldDirty: true });
		setFileNames((prev) => ({ ...prev, [slot]: undefined }));
		setFileErrors((prev) => ({ ...prev, [slot]: undefined }));
		setNotice("");
	}
	return {
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
	};
}