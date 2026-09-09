// 本文件读取本地图片并校验像素尺寸，返回供表单预览的 Data URI，并提供提交前剥离前缀的函数。
import { BRANDING_MAX_EDGE_PX, BRANDING_MAX_PIXELS } from "../../lib/schemas/branding";

/**
 * toUploadData 把草稿中的 Data URI 转成提交给服务端的裸 Base64；空串（移除）原样返回。
 * 不带前缀提交是因为浏览器对无法识别类型的文件会生成 data:application/octet-stream 头，
 * 服务端按头部 MIME 与图片实际格式比对会拒绝；去掉头部后服务端只按内容判断格式。
 */
export function toUploadData(draft: string): string {
	if (!draft.startsWith("data:")) return draft;
	const comma = draft.indexOf(",");
	return comma === -1 ? draft : draft.slice(comma + 1);
}

/** readImage 读取图片；读取失败、无法解码或尺寸超限时拒绝 Promise。 */
export function readImage(file: File): Promise<string> {
	return new Promise((resolve, reject) => {
		const reader = new FileReader();
		reader.onerror = () => reject(new Error("无法读取图片，请重试"));
		reader.onload = () => {
			const value = String(reader.result);
			const image = new Image();
			image.onerror = () => reject(new Error("图片损坏或无法预览，请选择有效的 PNG / JPEG"));
			image.onload = () => {
				if (
					image.naturalWidth > BRANDING_MAX_EDGE_PX ||
					image.naturalHeight > BRANDING_MAX_EDGE_PX ||
					image.naturalWidth * image.naturalHeight > BRANDING_MAX_PIXELS
				) {
					reject(new Error(`图片尺寸超过单边 ${BRANDING_MAX_EDGE_PX} 像素或总像素 ${BRANDING_MAX_PIXELS / 10000} 万`));
				} else resolve(value);
			};
			image.src = value;
		};
		reader.readAsDataURL(file);
	});
}