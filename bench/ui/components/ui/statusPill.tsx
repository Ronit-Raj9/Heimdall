import { cn } from "@/lib/utils";
import { cva } from "class-variance-authority";
import * as React from "react";

export type StatusTone = "ok" | "warn" | "bad" | "running" | "neutral";

const pillVariants = cva(
	"inline-flex w-fit shrink-0 items-center gap-1.5 rounded-full px-2 py-0.5 text-xs leading-4 font-medium whitespace-nowrap ring-1 ring-inset [&>svg]:size-3",
	{
		variants: {
			tone: {
				ok: "bg-chart-success/10 text-chart-success-ink ring-chart-success/25",
				warn: "bg-chart-warning/12 text-chart-warning-ink ring-chart-warning/30",
				bad: "bg-chart-error/10 text-chart-error-ink ring-chart-error/25",
				running: "bg-chart-info/10 text-chart-info-ink ring-chart-info/25",
				neutral: "bg-muted text-muted-foreground ring-border",
			},
		},
		defaultVariants: { tone: "neutral" },
	},
);

const dotTone: Record<StatusTone, string> = {
	ok: "bg-chart-success",
	warn: "bg-chart-warning",
	bad: "bg-chart-error",
	running: "bg-chart-info",
	neutral: "bg-muted-foreground/60",
};

/** A small colored dot; pulses when `pulse` is set (defaults on for running). */
export function StatusDot({ tone = "neutral", pulse, className }: { tone?: StatusTone; pulse?: boolean; className?: string }) {
	const live = pulse ?? tone === "running";
	return (
		<span data-slot="status-dot" aria-hidden="true" className={cn("relative inline-flex size-1.5 shrink-0", className)}>
			{live && <span className={cn("absolute inset-0 animate-ping rounded-full opacity-60 motion-reduce:hidden", dotTone[tone])} />}
			<span className={cn("relative inline-flex size-1.5 rounded-full", dotTone[tone])} />
		</span>
	);
}

export interface StatusPillProps extends Omit<React.ComponentProps<"span">, "children"> {
	tone?: StatusTone;
	children?: React.ReactNode;
	/** Leading dot. Default true. Set false when passing an icon as a child. */
	dot?: boolean;
	/** Pulse the dot. Defaults to true for tone "running". */
	pulse?: boolean;
	testId?: string;
}

/** Status as color + dot + one short word ("Ready", "Failed", "Running"). */
export function StatusPill({ tone = "neutral", dot = true, pulse, className, children, testId, ...props }: StatusPillProps) {
	return (
		<span data-slot="status-pill" data-tone={tone} data-testid={testId} className={cn(pillVariants({ tone }), className)} {...props}>
			{dot && <StatusDot tone={tone} pulse={pulse} />}
			{children}
		</span>
	);
}