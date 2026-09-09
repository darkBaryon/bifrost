# 企业表单校验规则

## 功能

集中定义企业页面使用的表单校验规则。目前包含品牌图片上传限制和品牌编辑草稿的结构。

## 架构

本目录使用 Zod 描述输入要求，供[品牌设置页面](../../components/branding/README.md)使用，不负责界面渲染或发送请求。

`brandingFileSchema` 检查文件大小和声明类型；图片的实际解码和像素校验由页面的 `image.ts` 完成。`brandingFormSchema` 校验待提交草稿：缺字段保持原图，空字符串表示移除。

上传限制与[服务端校验](../../../../../transports/bifrost-http/handlers/branding/validation.go)保持一致。前端校验用于及时提示，服务端仍独立校验请求。

## 文件说明

| 文件 | 职责 |
|---|---|
| [branding.ts](branding.ts) | 定义上传大小、像素和 Data URI 长度限制，导出文件校验、草稿校验及 BrandingForm 类型。 |
