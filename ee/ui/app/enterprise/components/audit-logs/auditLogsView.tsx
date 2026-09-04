import { ScrollText } from "lucide-react";

// ee 覆盖: 取代上游 _fallbacks 里的 "联系我们" 占位. 骨架期只证明 @enterprise 覆盖链路可用;
// B1 审计日志在此换成真实列表页 (逻辑一律走 /api/ee/* 接口, 页面不承载逻辑).
export default function AuditLogsView() {
	return (
		<div className="flex h-full w-full flex-col items-center justify-center gap-4 text-center" data-testid="ee-audit-logs-placeholder">
			<ScrollText className="text-muted-foreground h-[5.5rem] w-[5.5rem]" strokeWidth={1} />
			<h1 className="text-muted-foreground text-xl font-medium">审计日志(ee)即将到来</h1>
			<p className="text-muted-foreground max-w-[600px] text-sm">
				这是 ee 包壳骨架的占位页面：它来自 ee/ui 的覆盖层，而不是上游的企业版占位。B1 审计日志会在这里落地。
			</p>
		</div>
	);
}
