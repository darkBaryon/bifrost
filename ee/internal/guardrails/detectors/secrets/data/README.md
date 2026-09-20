# 密钥规则数据

`rules.json` 由 Gitleaks v8.30.1 的 `config/gitleaks.toml` 转换而来，再合入 `rules.extra.json` 中本仓的修正与新增；许可见 `NOTICE`。**不要手改 `rules.json`**，改 `rules.extra.json` 后重新生成：

```bash
git clone --depth 1 --branch v8.30.1 https://github.com/gitleaks/gitleaks /tmp/gitleaks
python3 ee/scripts/secrets-rules-convert.py /tmp/gitleaks/config/gitleaks.toml \
  --extra ee/internal/guardrails/detectors/secrets/data/rules.extra.json \
  --out   ee/internal/guardrails/detectors/secrets/data/rules.json
```

转换时丢弃按文件路径判断的规则与条件（对话文本没有路径）。等级：`generic-` 开头的规则为 medium（无法归属厂商，可能误报），其余为 high。

`rules.extra.json` 的修正依据是 [密钥格式核实](../../../../../../product/需求/内容安全/密钥格式核实.md)：Gemini 新版 `AQ.` key、Anthropic 通配、OpenAI 不定长、阿里云 ID 长度放宽；新增国内外厂商专用规则与钉钉 / 飞书 / 企业微信 webhook；通用规则补中文字段名（密钥、秘钥、密码、令牌、口令）与中文分隔符。长度一律写「前缀 + 最小长度」，因为多数厂商只公布前缀。
