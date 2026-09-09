import { baseApi } from "./baseApi";

/**
 * 企业品牌设置的接口响应。查询使用 POST，保持公开以供未登录页面读取；写入沿用管理端鉴权。
 * OSS 不提供这些接口，由 useBranding 跳过查询并使用默认图片。
 * 响应只携带图片 URL，浏览器通过 GET 读取和缓存图片。
 */
export interface BrandingState {
	/** True when at least one slot is overridden. */
	enabled: boolean;
	has_logo: boolean;
	has_icon: boolean;
	/** Content-versioned; empty when the slot uses the Bifrost default. */
	logo_url?: string;
	icon_url?: string;
	updated_at?: string;
}

/**
 * Upload payload, applied as a merge:
 *
 *   omitted        -> leave that slot as stored
 *   ""             -> clear that slot, restoring the Bifrost default
 *   base64 data    -> replace that slot
 *
 * So a caller changing only the icon omits the logo fields rather than
 * re-uploading the logo to avoid wiping it.
 */
export interface BrandingPayload {
	logo?: string; // base64 image data (a full data: URI is also accepted)
	logo_mime?: string;
	icon?: string;
	icon_mime?: string;
}

export const brandingApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getBranding: builder.query<BrandingState, void>({
			query: () => ({
				url: "/branding/get",
				method: "POST",
			}),
			providesTags: ["Branding"],
		}),

		updateBranding: builder.mutation<BrandingState, BrandingPayload>({
			query: (data) => ({
				url: "/branding/update",
				method: "POST",
				body: data,
			}),
			invalidatesTags: ["Branding"],
		}),

		// 清空品牌设置，恢复默认 Logo 和小图标。
		resetBranding: builder.mutation<BrandingState, void>({
			query: () => ({
				url: "/branding/reset",
				method: "POST",
			}),
			invalidatesTags: ["Branding"],
		}),
	}),
});

export const { useGetBrandingQuery, useUpdateBrandingMutation, useResetBrandingMutation } = brandingApi;