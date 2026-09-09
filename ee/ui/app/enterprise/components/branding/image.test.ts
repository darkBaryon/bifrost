// 本文件验证读图的异步成功、读取失败、解码失败和尺寸边界。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readImage, toUploadData } from "./image";

// 替身只模拟浏览器读文件和解码的完成事件，校验逻辑仍执行真实 readImage。
let width = 96;
let height = 32;
let failure: "read" | "decode" | undefined;
const dataURI = "data:image/png;base64,dGVzdA==";

beforeEach(() => {
	width = 96;
	height = 32;
	failure = undefined;
	vi.stubGlobal(
		"FileReader",
		class {
			result = dataURI;
			onerror?: () => void;
			onload?: () => void;
			readAsDataURL() {
				queueMicrotask(() => (failure === "read" ? this.onerror?.() : this.onload?.()));
			}
		},
	);
	vi.stubGlobal(
		"Image",
		class {
			naturalWidth = width;
			naturalHeight = height;
			onerror?: () => void;
			onload?: () => void;
			set src(_value: string) {
				queueMicrotask(() => (failure === "decode" ? this.onerror?.() : this.onload?.()));
			}
		},
	);
});

afterEach(() => vi.unstubAllGlobals());

describe("品牌图片读取", () => {
	it.each([
		[96, 32],
		[4096, 1],
		[2000, 2000],
	])("接受允许范围和边界尺寸 %i × %i", async (w, h) => {
		width = w;
		height = h;
		await expect(readImage(new File(["image"], "logo.png"))).resolves.toBe(dataURI);
	});

	it.each([
		[4097, 1],
		[1, 4097],
		[2000, 2001],
	])("拒绝超过单边或总像素上限 %i × %i", async (w, h) => {
		width = w;
		height = h;
		await expect(readImage(new File(["image"], "logo.png"))).rejects.toThrow("图片尺寸超过");
	});

	it("文件读取失败时返回读取错误", async () => {
		failure = "read";
		await expect(readImage(new File(["image"], "logo.png"))).rejects.toThrow("无法读取图片，请重试");
	});

	it("图片解码失败时返回损坏提示", async () => {
		failure = "decode";
		await expect(readImage(new File(["image"], "logo.png"))).rejects.toThrow("图片损坏或无法预览");
	});
});

describe("提交数据转换", () => {
	it("剥离任意 Data URI 前缀，只保留 Base64", () => {
		expect(toUploadData("data:image/png;base64,dGVzdA==")).toBe("dGVzdA==");
		expect(toUploadData("data:application/octet-stream;base64,dGVzdA==")).toBe("dGVzdA==");
	});
	it("空串表示移除，裸 Base64 原样返回", () => {
		expect(toUploadData("")).toBe("");
		expect(toUploadData("dGVzdA==")).toBe("dGVzdA==");
	});
});