# 开发用假检测器

[detector.go](detector.go) 实现 `guardrails.Detector`：文本包含 `[guardrails-test]` 时返回高风险，其他文本返回空结果，请求取消时返回取消错误。它只用于联调，不具备真实安全检测能力。

由 `app/guardrails.go` 在显式设置 `EE_GUARDRAILS_FAKE` 时装配。启动方式与三种测试模式见[内容安全框架说明](../../README.md#开发联调)。
