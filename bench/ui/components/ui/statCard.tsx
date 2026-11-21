import { cn } from "@/lib/utils";
import { ArrowDownRight, ArrowUpRight, type LucideIcon, Minus } from "lucide-react";
import * as React from "react";

export interface StatTrend {
	/** Shown next to the arrow, e.g. "+12%" or "3 more". */
	value: React.ReactNode;
	direction: "up" | "down" | "flat";
	/** Whether this direction is good news. Default: up is good, down is bad. */
	positive?: boolean;
}

export interface StatCardProps {
	label: React.ReactNode;
	value: React.ReactNode;
	hint?: React.ReactNode;
	trend?: StatTrend;
	icon?: LucideIcon;
	className?: string;
	testId?: string;
}

/** One headline number: small label, big tabular value, optional hint and trend. */
export function StatCard({ label, value, hint, trend, icon: Icon, className, testId }: StatCardProps) {
	const good = trend ? (trend.positive ?? trend.direction === "up") : false;
	const TrendIcon = trend?.direction === "up" ? ArrowUpRight : trend?.direction === "down" ? ArrowDownRight : Minus;
	return (
		<div
			data-slot="stat-card"
			data-testid={testId}
			className={cn(
				"bg-card text-card-foreground flex min-w-0 flex-col gap-1.5 rounded-xl border border-border/70 p-5 shadow-(--shadow-card)",
				className,
			)}
		>
			<div className="text-muted-foreground flex items-center justify-between gap-2 text-sm">
				<span className="truncate">{label}</span>
				{Icon && <Icon className="size-4 shrink-0 opacity-70" aria-hidden="true" />}
			</div>
			<div className="text-foreground truncate text-2xl font-semibold tracking-tight tabular-nums">{value}</div>
			{(hint || trend) && (
				<div className="text-muted-foreground flex min-w-0 items-center gap-2 text-xs">
					{trend && (
						<span
							data-slot="stat-trend"
							data-direction={trend.direction}
							className={cn(
								"inline-flex shrink-0 items-center gap-0.5 font-medium",
								trend.direction === "flat" ? "text-muted-foreground" : good ? "text-chart-success-ink" : "text-chart-error-ink",
							)}
						>
							<TrendIcon className="size-3.5" aria-hidden="true" />
							{trend.value}
						</span>
					)}
					{hint && <span className="truncate">{hint}</span>}
				</div>
			)}
		</div>
	);
}

/** Responsive grid for a row of StatCards (1 col on phones, up to 4). */
export function StatGrid({ className, ...props }: React.ComponentProps<"div">) {
	return <div data-slot="stat-grid" className={cn("grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4", className)} {...props} />;
}