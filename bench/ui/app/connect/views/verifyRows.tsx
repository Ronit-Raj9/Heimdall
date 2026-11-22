import { Badge } from "@/components/ui/badge";
import type { VerifyRow } from "@/lib/types/connect";
import { cn } from "@/lib/utils";
import { CircleCheck, CircleDashed, CircleMinus, CircleX, Loader2, TriangleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";
import { rowTone } from "./connect.utils";
import { FailureNote } from "./failureNote";

function RowIcon({ status }: { status: VerifyRow["status"] }) {
	const cls = "size-4 shrink-0";
	switch (status) {
		case "pass":
			return <CircleCheck className={cn(cls, "text-chart-success-ink")} aria-hidden />;
		case "warn":
			return <TriangleAlert className={cn(cls, "text-chart-warning-ink")} aria-hidden />;
		case "fail":
			return <CircleX className={cn(cls, "text-chart-error-ink")} aria-hidden />;
		case "running":
			return <Loader2 className={cn(cls, "text-chart-info-ink animate-spin")} aria-hidden />;
		case "skip":
			return <CircleMinus className={cn(cls, "text-muted-foreground")} aria-hidden />;
		default:
			return <CircleDashed className={cn(cls, "text-muted-foreground")} aria-hidden />;
	}
}

/** The verification rows as they stream in: status, detail, and the failure with its fix. */
export function VerifyRows({ rows, onPick }: { rows: VerifyRow[]; onPick?: (path: string) => void }) {
	const { t } = useTranslation("connect");
	return (
		<ol className="flex flex-col divide-y rounded-lg border" data-testid="verify-rows">
			{rows.map((row) => (
				<li key={row.id} className="flex flex-col gap-2 p-3" data-testid={`verify-row-${row.id}`} data-status={row.status}>
					<div className="flex items-center gap-3 text-sm">
						<RowIcon status={row.status} />
						<span className="w-44 shrink-0 font-medium">{row.title}</span>
						<Badge variant={rowTone(row.status)}>{t(`verify.status.${row.status}`)}</Badge>
						<span className="text-muted-foreground min-w-0 flex-1 truncate" title={row.detail}>
							{row.detail}
						</span>
						{row.ms ? <span className="text-muted-foreground text-xs tabular-nums">{row.ms} ms</span> : null}
					</div>
					{row.failure && (row.status === "fail" || row.status === "warn") && <FailureNote failure={row.failure} onPick={onPick} />}
				</li>
			))}
		</ol>
	);
}