import { cn } from "@/lib/utils";
import { CircleAlert, CircleCheck, Info, TriangleAlert, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

export type AlertTone = "info" | "warning" | "danger" | "success";

const TONE: Record<AlertTone, { box: string; icon: LucideIcon }> = {
	info: { box: "border-info/30 bg-info-soft text-info-ink", icon: Info },
	warning: { box: "border-warning/40 bg-warning-soft text-warning-ink", icon: TriangleAlert },
	danger: { box: "border-danger/30 bg-danger-soft text-danger-ink", icon: CircleAlert },
	success: { box: "border-success/30 bg-success-soft text-success-ink", icon: CircleCheck },
};

/**
 * The one callout (X-98): a full border, a soft tint of its tone, an icon and the text; never a
 * coloured side stripe. Warning is for something to act on now; danger only for a real failure.
 */
export function Alert({
	tone,
	title,
	children,
	action,
	className,
	testId,
}: {
	tone: AlertTone;
	title?: ReactNode;
	children?: ReactNode;
	action?: ReactNode;
	className?: string;
	testId?: string;
}) {
	const { box, icon: Icon } = TONE[tone];
	return (
		<div
			role={tone === "danger" || tone === "warning" ? "alert" : "status"}
			data-slot="alert"
			data-tone={tone}
			data-testid={testId}
			className={cn("flex gap-3 rounded-lg border p-3 text-sm", box, className)}
		>
			<Icon className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
			<div className="flex min-w-0 flex-1 flex-col gap-1">
				{title && <p className="font-medium">{title}</p>}
				{children && <div className="text-foreground">{children}</div>}
			</div>
			{action && <div className="flex shrink-0 items-start">{action}</div>}
		</div>
	);
}
