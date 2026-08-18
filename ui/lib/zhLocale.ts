// 运行时中文化层:不改任何组件源码,通过 MutationObserver 在 DOM 层把英文文案
// 替换为中文。未收录的词条保持英文显示,补充 DICT 即可,永不影响功能。
// 关闭方式:localStorage.setItem("bf-lang", "en") 后刷新。

const DICT: Record<string, string> = {
	// ===== 侧边栏导航 =====
	"Dashboard": "仪表盘",
	"Observability": "可观测性",
	"LLM Logs": "LLM 日志",
	"Logs": "日志",
	"Evals": "评测",
	"Alerting": "告警",
	"Rules": "规则",
	"Channels": "通知渠道",
	"History": "历史记录",
	"Governance": "治理",
	"Virtual Keys": "虚拟密钥",
	"Budgets & Limits": "预算与限额",
	"Teams": "团队",
	"Customers": "客户",
	"Business Units": "业务单元",
	"Approvals": "审批",
	"Model Providers": "模型供应商",
	"Providers": "供应商",
	"Model Catalog": "模型目录",
	"Models": "模型",
	"Model Settings": "模型设置",
	"Pricing Overrides": "定价覆盖",
	"Routing Rules": "路由规则",
	"Adaptive Routing": "自适应路由",
	"Complexity Router": "复杂度路由",
	"Circuit Breaker": "熔断器",
	"Caching": "缓存",
	"Plugins": "插件",
	"Guardrails": "护栏",
	"MCP Gateway": "MCP 网关",
	"MCP Catalog": "MCP 目录",
	"MCP Library": "MCP 库",
	"MCP Logs": "MCP 日志",
	"MCP Settings": "MCP 设置",
	"Tool Groups": "工具组",
	"Prompt Repository": "提示词仓库",
	"Skills Repository": "技能仓库",
	"Webhooks": "Webhook",
	"Users": "用户",
	"Roles & Permissions": "角色与权限",
	"User Provisioning": "用户同步",
	"Access Profiles": "访问档案",
	"Audit Logs": "审计日志",
	"Auth Sessions": "认证会话",
	"OAuth Grants": "OAuth 授权",
	"Devices": "设备",
	"Settings": "设置",
	"Security": "安全",
	"Branding": "品牌定制",
	"Client Settings": "客户端设置",
	"Logs Settings": "日志设置",
	"Performance Tuning": "性能调优",
	"Compatibility": "兼容性",
	"Proxy": "代理",
	"License Info": "许可证信息",
	"Cluster Config": "集群配置",
	"Edge Control": "Edge 管控",
	"Edge Settings": "Edge 设置",
	"Feature Flags": "功能开关",
	"Connectors": "连接器",
	"API Keys": "API 密钥",

	// ===== 通用操作 =====
	"Save": "保存",
	"Cancel": "取消",
	"Delete": "删除",
	"Edit": "编辑",
	"Add": "添加",
	"Create": "创建",
	"Update": "更新",
	"Search": "搜索",
	"Filter": "筛选",
	"Refresh": "刷新",
	"Close": "关闭",
	"Confirm": "确认",
	"Apply": "应用",
	"Copy": "复制",
	"Copied": "已复制",
	"Download": "下载",
	"Export": "导出",
	"Import": "导入",
	"Submit": "提交",
	"Reset": "重置",
	"Back": "返回",
	"Next": "下一步",
	"Previous": "上一步",
	"Done": "完成",
	"View": "查看",
	"Details": "详情",
	"Retry": "重试",
	"Remove": "移除",
	"Duplicate": "复制副本",
	"Enable": "启用",
	"Disable": "禁用",
	"Test": "测试",
	"Connect": "连接",
	"Disconnect": "断开连接",
	"Learn more": "了解更多",
	"Documentation": "文档",
	"Docs": "文档",
	"Get Started": "开始使用",
	"Save Changes": "保存更改",
	"Discard": "放弃",
	"Continue": "继续",
	"Loading...": "加载中…",
	"Saving...": "保存中…",
	"Show more": "展开更多",
	"Show less": "收起",
	"Select all": "全选",
	"Clear": "清空",
	"Clear all": "全部清空",
	"Sign out": "退出登录",
	"Sign in": "登录",
	"Log in": "登录",
	"Log out": "退出登录",
	"Login": "登录",

	// ===== 表格 / 分页 =====
	"Name": "名称",
	"Description": "描述",
	"Status": "状态",
	"Actions": "操作",
	"Type": "类型",
	"Value": "值",
	"Key": "密钥",
	"Model": "模型",
	"Provider": "供应商",
	"Created": "创建时间",
	"Created At": "创建时间",
	"Updated": "更新时间",
	"Updated At": "更新时间",
	"Last Updated": "最近更新",
	"Rows per page": "每页行数",
	"No results": "暂无数据",
	"No results found": "未找到结果",
	"No data": "暂无数据",
	"of": "共",
	"Page": "页",
	"per page": "条/页",
	"Total": "总计",
	"Weight": "权重",
	"Priority": "优先级",
	"Enabled": "已启用",
	"Disabled": "已禁用",
	"Active": "活跃",
	"Inactive": "未激活",
	"Never": "从不",
	"None": "无",
	"All": "全部",
	"Yes": "是",
	"No": "否",
	"Unknown": "未知",
	"Optional": "可选",
	"Required": "必填",
	"Default": "默认",
	"Custom": "自定义",
	"Advanced": "高级",
	"General": "常规",
	"Overview": "概览",

	// ===== 状态与提示 =====
	"Success": "成功",
	"Error": "错误",
	"Warning": "警告",
	"Failed": "失败",
	"Pending": "等待中",
	"Running": "运行中",
	"Completed": "已完成",
	"Cancelled": "已取消",
	"Expired": "已过期",
	"Healthy": "健康",
	"Degraded": "降级",
	"Restart Required": "需要重启",
	"Setup checklist incomplete": "初始化清单未完成",
	"Need help with production setup?": "需要生产环境部署帮助?",
	"Are you sure?": "确定要执行此操作吗?",
	"This action cannot be undone.": "此操作无法撤销。",

	// ===== 仪表盘 / 日志 =====
	"Requests": "请求数",
	"Total Requests": "总请求数",
	"Error Rate": "错误率",
	"Success Rate": "成功率",
	"Latency": "延迟",
	"Avg Latency": "平均延迟",
	"Cost": "成本",
	"Total Cost": "总成本",
	"Tokens": "Token 数",
	"Input Tokens": "输入 Token",
	"Output Tokens": "输出 Token",
	"Cache Hit": "缓存命中",
	"Cached": "已缓存",
	"Timestamp": "时间",
	"Duration": "耗时",
	"Request": "请求",
	"Response": "响应",
	"Stream": "流式",
	"Object": "对象",
	"Endpoint": "端点",
	"Last 24 hours": "最近 24 小时",
	"Last 7 days": "最近 7 天",
	"Last 30 days": "最近 30 天",

	// ===== 供应商 / 密钥管理 =====
	"Add Provider": "添加供应商",
	"Add Key": "添加密钥",
	"Add API Key": "添加 API 密钥",
	"New Key": "新建密钥",
	"Base URL": "Base URL",
	"API Key": "API 密钥",
	"Keys": "密钥",
	"Models Allowed": "允许的模型",
	"Allowed Models": "允许的模型",
	"Add Model": "添加模型",
	"Refresh Models": "刷新模型列表",
	"Network Config": "网络配置",
	"Concurrency": "并发数",
	"Buffer Size": "缓冲区大小",
	"Max Retries": "最大重试次数",
	"Timeout": "超时",
	"Add Virtual Key": "添加虚拟密钥",
	"Budget": "预算",
	"Rate Limit": "限流",
	"Rate Limits": "限流",
	"Usage": "用量",
	"Quota": "配额",
	"Expires": "过期时间",
	"Configuration": "配置",
	"Config": "配置",

	// ===== 仪表盘标签页与图卡 =====
	"Provider Usage": "供应商用量",
	"Model Rankings": "模型排行",
	"MCP usage": "MCP 用量",
	"MCP Usage": "MCP 用量",
	"Team Rankings": "团队排行",
	"User Rankings": "用户排行",
	"Virtual Key Rankings": "虚拟密钥排行",
	"Customer Rankings": "客户排行",
	"Request Volume": "请求量",
	"Token Usage": "Token 用量",
	"External Cache Hit Rate": "外部缓存命中率",
	"Local Cache Hit Rate": "本地缓存命中率",
	"Model Usage": "模型用量",
	"Throughput": "吞吐量",
	"No data available": "暂无数据",
	"All Models": "全部模型",
	"All Providers": "全部供应商",
	"Input": "输入",
	"Output": "输出",
	"Last hour": "最近 1 小时",
	"Last Hour": "最近 1 小时",
	"Last 6 hours": "最近 6 小时",
	"Last 12 hours": "最近 12 小时",

	// ===== 筛选侧栏 =====
	"Filters": "筛选",
	"Processing": "处理中",
	"Selected Keys": "已选密钥",
	"App": "应用",
	"Aliases": "别名",
	"Routing Engines": "路由引擎",
	"Local Caching": "本地缓存",
	"User": "用户",
	"Search or add a model": "搜索或添加模型",
	"Search...": "搜索…",
};

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

function translateRaw(raw: string): string | null {
	const trimmed = raw.trim();
	if (!trimmed) return null;
	const hit = lookup(trimmed);
	if (hit === undefined || hit === trimmed) return null;
	return raw.replace(trimmed, hit);
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
			const hit = DICT[v.trim()];
			if (hit) el.setAttribute(attr, hit);
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
				}
				m.addedNodes.forEach((n) => walk(n));
			}
		});
		observer.observe(document.body, { childList: true, subtree: true, characterData: true });
	};

	if (document.readyState === "loading") {
		document.addEventListener("DOMContentLoaded", start);
	} else {
		start();
	}
}
