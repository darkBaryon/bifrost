// 运行时中文化层:不改任何组件源码,通过 MutationObserver 在 DOM 层把英文文案
// 替换为中文。未收录的词条保持英文显示,补充 DICT 即可,永不影响功能。
// 关闭方式:localStorage.setItem("bf-lang", "en") 后刷新。

import DICT_JSON from "./zhDict.json";

// 词条字典:纯数据,一行一条,键按字母排序,存放于 zhDict.json(新增/修改词条直接编辑该文件)。
// 动态句进 RULES(见下),两者由 scripts/zh-coverage.mjs 机械对账。
const DICT: Record<string, string> = DICT_JSON;

const SKIP_TAGS = new Set(["SCRIPT", "STYLE", "CODE", "PRE", "TEXTAREA", "NOSCRIPT"]);
const ATTRS = ["placeholder", "title", "aria-label"];

// 大小写兜底:UI 里大量小写枚举值(如 status 的 "success")靠 CSS capitalize
// 显示成首字母大写,因此查表同时登记原键与全小写键。
const LOOKUP = new Map<string, string>();
for (const [k, v] of Object.entries(DICT)) {
	LOOKUP.set(k, v);
	const lower = k.toLowerCase();
	if (!LOOKUP.has(lower)) LOOKUP.set(lower, v);
}

function lookup(s: string): string | undefined {
	return LOOKUP.get(s) ?? LOOKUP.get(s.toLowerCase());
}


// 动态句规则层(方案 v4 §4):四字段契约,skeleton/sample 供 zh-coverage 与 self-test 机械互证。
// 纪律:re 必须 ^…$ 锚定;禁止 (.+) 级全泛化捕获;out 不得保留可再匹配的英文锚;上限 100 条。
type ZhRule = { re: RegExp; out: string; skeleton: string; sample: string };
const RULES: ZhRule[] = [
	{ re: /^(\d+) model budgets?$/, out: "$1 个模型预算",
		skeleton: "⟨x⟩ model budget⟨x⟩", sample: "3 model budgets" },
	{ re: /^in (\d+) minutes?$/, out: "$1 分钟后",
		skeleton: "in ⟨x⟩ minutes", sample: "in 5 minutes" },
	{ re: /^in (\d+) min$/, out: "$1 分钟后",
		skeleton: "in ⟨x⟩ min", sample: "in 5 min" },
	{ re: /^in (\d+) hours?$/, out: "$1 小时后",
		skeleton: "in ⟨x⟩ hours", sample: "in 2 hours" },
	{ re: /^in (\d+) days?$/, out: "$1 天后",
		skeleton: "in ⟨x⟩ days", sample: "in 3 days" },
	{ re: /^(\d+) min ago$/, out: "$1 分钟前",
		skeleton: "⟨x⟩ min ago", sample: "5 min ago" },
	{ re: /^Exported (\d+) virtual keys?$/, out: "已导出 $1 个虚拟密钥",
		skeleton: "Exported ⟨x⟩ virtual keys", sample: "Exported 3 virtual keys" },
	{ re: /^Rotated (\d+) virtual keys?$/, out: "已轮换 $1 个虚拟密钥",
		skeleton: "Rotated ⟨x⟩ virtual keys", sample: "Rotated 3 virtual keys" },
	{ re: /^Rotated (\d+) virtual keys?\. (\d+) failed\.$/, out: "已轮换 $1 个虚拟密钥,$2 个失败。",
		skeleton: "Rotated ⟨x⟩ virtual keys. ⟨x⟩ failed.", sample: "Rotated 3 virtual keys. 1 failed." },
	{ re: /^HTTP error! status: (\d+)$/, out: "HTTP 错误,状态码 $1",
		skeleton: "HTTP error! status: ⟨x⟩", sample: "HTTP error! status: 500" },
	{ re: /^Must be at least (\d+)$/, out: "不得小于 $1",
		skeleton: "Must be at least ⟨x⟩", sample: "Must be at least 1" },
	{ re: /^Must be at most (\d+)$/, out: "不得大于 $1",
		skeleton: "Must be at most ⟨x⟩", sample: "Must be at most 10" },
	{ re: /^Must be at least (\d+) characters?$/, out: "至少 $1 个字符",
		skeleton: "Must be at least ⟨x⟩ characters", sample: "Must be at least 8 characters" },
	{ re: /^Must be at most (\d+) characters?$/, out: "至多 $1 个字符",
		skeleton: "Must be at most ⟨x⟩ characters", sample: "Must be at most 64 characters" },
	{ re: /^Must have at least (\d+) items?$/, out: "至少 $1 项",
		skeleton: "Must have at least ⟨x⟩ items", sample: "Must have at least 1 items" },
	{ re: /^Must have at most (\d+) items?$/, out: "至多 $1 项",
		skeleton: "Must have at most ⟨x⟩ items", sample: "Must have at most 5 items" },
	{ re: /^Attempt Trail \((\d+) attempts?\)$/, out: "尝试轨迹($1 次)",
		skeleton: "Attempt Trail (⟨x⟩ attempts)", sample: "Attempt Trail (3 attempts)" },
	{ re: /^Available Tools \((\d+)\)$/, out: "可用工具($1)",
		skeleton: "Available Tools (⟨x⟩)", sample: "Available Tools (5)" },
	{ re: /^Caching Details \((\d+)\)$/, out: "缓存详情($1)",
		skeleton: "Caching Details (⟨x⟩)", sample: "Caching Details (2)" },
	{ re: /^List Models Output \((\d+)\)$/, out: "模型列表输出($1)",
		skeleton: "List Models Output (⟨x⟩)", sample: "List Models Output (12)" },
	{ re: /^Rerank Output \((\d+)\)$/, out: "重排序输出($1)",
		skeleton: "Rerank Output (⟨x⟩)", sample: "Rerank Output (10)" },
	{ re: /^Video List Output \((\d+)\)$/, out: "视频列表输出($1)",
		skeleton: "Video List Output (⟨x⟩)", sample: "Video List Output (4)" },
	{ re: /^Description exceeds 1024 character limit \((\d+)\/1024\)$/, out: "描述超出 1024 字符上限($1/1024)",
		skeleton: "Description exceeds 1024 character limit (⟨x⟩/1024)", sample: "Description exceeds 1024 character limit (1100/1024)" },
	{ re: /^Dropdown opened\. (\d+) options available\. Use arrow keys to navigate\.$/, out: "下拉已打开,$1 个选项可用,方向键导航。",
		skeleton: "Dropdown opened. ⟨x⟩ options available. Use arrow keys to navigate.", sample: "Dropdown opened. 8 options available. Use arrow keys to navigate." },
	{ re: /^Option removed\. (\d+) of (\d+) options selected\.$/, out: "已移除选项,已选 $1/$2。",
		skeleton: "Option removed. ⟨x⟩ of ⟨x⟩ options selected.", sample: "Option removed. 2 of 5 options selected." },
	{ re: /^(\d+) of (\d+) tool executions failed$/, out: "$1/$2 个工具执行失败",
		skeleton: "⟨x⟩ of ⟨x⟩ tool executions failed", sample: "1 of 4 tool executions failed" },
	{ re: /^(\d+) budget overrides?$/, out: "$1 条预算覆盖",
		skeleton: "⟨x⟩ budget overrides", sample: "3 budget overrides" },
	{ re: /^(\d+) options selected\. (\d+) of (\d+) total selected\.$/, out: "已选 $1 项($2/$3)。",
		skeleton: "⟨x⟩ options selected. ⟨x⟩ of ⟨x⟩ total selected.", sample: "2 options selected. 2 of 9 total selected." },
	{ re: /^Select all (\d+) options?$/, out: "全选 $1 项",
		skeleton: "Select all ⟨x⟩ options", sample: "Select all 5 options" },
	{ re: /^Clear all (\d+) selected options?$/, out: "清除全部 $1 个已选项",
		skeleton: "Clear all ⟨x⟩ selected options", sample: "Clear all 3 selected options" },
	{ re: /^Interval must be 0 \(disabled\) or at least (\d+) minutes?$/, out: "间隔须为 0(禁用)或至少 $1 分钟",
		skeleton: "Interval must be 0 (disabled) or at least ⟨x⟩ minute", sample: "Interval must be 0 (disabled) or at least 1 minute" },
	{ re: /^Target weights must sum to 1, current total: (\d+(?:\.\d+)?)$/, out: "目标权重之和须为 1,当前合计 $1",
		skeleton: "Target weights must sum to 1, current total: ⟨x⟩", sample: "Target weights must sum to 1, current total: 0.8" },
	{ re: /^Budget #(\d+): Maximum Spend \(USD\)$/, out: "预算 #$1:最大花费(USD)",
		skeleton: "Budget #⟨x⟩: Maximum Spend (USD)", sample: "Budget #1: Maximum Spend (USD)" },
	{ re: /^(v[\d.]+) is now available\.$/, out: "$1 现已发布。",
		skeleton: "⟨x⟩ is now available.", sample: "v2.0.0 is now available." },
];

function translateRaw(raw: string): string | null {
	const trimmed = raw.trim();
	if (!trimmed) return null;
	const hit = lookup(trimmed);
	if (hit !== undefined) {
		if (hit === trimmed) return null; // 收敛契约①
		return raw.replace(trimmed, hit);
	}
	for (const r of RULES) {
		if (r.re.test(trimmed)) {
			const out = trimmed.replace(r.re, r.out);
			if (out === trimmed) return null; // 收敛契约①
			return raw.replace(trimmed, out);
		}
	}
	return null;
}

function walk(node: Node): void {
	if (node.nodeType === Node.TEXT_NODE) {
		const t = translateRaw(node.nodeValue ?? "");
		if (t !== null) node.nodeValue = t;
		return;
	}
	if (node.nodeType !== Node.ELEMENT_NODE) return;
	const el = node as Element;
	if (SKIP_TAGS.has(el.tagName)) return;
	// Monaco 编辑器内部不碰
	if (el.classList.contains("monaco-editor")) return;
	for (const attr of ATTRS) {
		const v = el.getAttribute(attr);
		if (v) {
			const t = translateRaw(v); // 与文本通道同管线(评审1-2.2)
			if (t !== null) el.setAttribute(attr, t);
		}
	}
	for (let i = 0; i < el.childNodes.length; i++) walk(el.childNodes[i]);
}

/** 安装中文化层。翻译后的文本不会再命中字典,因此不会无限循环。 */
export function installZhLocale(): void {
	if (typeof window === "undefined") return;
	if (window.localStorage.getItem("bf-lang") === "en") return;

	const start = () => {
		walk(document.body);
		const observer = new MutationObserver((mutations) => {
			for (const m of mutations) {
				if (m.type === "characterData") {
					const t = translateRaw(m.target.nodeValue ?? "");
					if (t !== null) m.target.nodeValue = t;
					continue;
				}
				if (m.type === "attributes" && m.target instanceof Element && m.attributeName) {
					const v = m.target.getAttribute(m.attributeName);
					if (v) {
						const t = translateRaw(v); // 契约①防回写环(评审2-N1)
						if (t !== null) m.target.setAttribute(m.attributeName, t);
					}
					continue;
				}
				m.addedNodes.forEach((n) => walk(n));
			}
		});
		observer.observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: [...ATTRS] });
	};

	if (document.readyState === "loading") {
		document.addEventListener("DOMContentLoaded", start);
	} else {
		start();
	}
}
