// 本文件定义品牌上传与表单校验规则，供企业品牌设置使用。
import { z } from "zod";

/** 上传限制与服务端 ee/transports/bifrost-http/handlers/branding/validation.go 保持一致。 */
export const BRANDING_MAX_BYTES = 1024 * 1024;
export const BRANDING_MAX_MIB = BRANDING_MAX_BYTES / 1024 / 1024;
export const BRANDING_MAX_EDGE_PX = 4096;
export const BRANDING_MAX_PIXELS = 4_000_000;
/** Data URI 的最大长度：Base64 补齐后的长度加上允许格式中最长的前缀。 */
export const BRANDING_MAX_DATA_URI_LENGTH = Math.ceil(BRANDING_MAX_BYTES / 3) * 4 + "data:image/jpeg;base64,".length;

/** brandingFileSchema 检查本地文件大小和声明类型；图片内容仍需独立解码校验。 */
export const brandingFileSchema = z.object({
	size: z.number().positive("图片不能为空").max(BRANDING_MAX_BYTES, `每张图片不能超过 ${BRANDING_MAX_MIB} MiB`),
	type: z.string().refine((value) => ["image/png", "image/jpeg", ""].includes(value), "请选择 PNG 或 JPEG 图片"),
});
/** brandingFormSchema 校验提交草稿；缺字段保留原图，空串表示移除。 */
export const brandingFormSchema = z.object({
	logo: z.string().max(BRANDING_MAX_DATA_URI_LENGTH, "Logo 文件过大").optional(),
	icon: z.string().max(BRANDING_MAX_DATA_URI_LENGTH, "小图标文件过大").optional(),
});
/** BrandingForm 是尚未保存的图片草稿。 */
export type BrandingForm = z.infer<typeof brandingFormSchema>;