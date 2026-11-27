import { statusOf } from "@/lib/status/vocabulary";
import { cn } from "@/lib/utils";
import { cva } from "class-variance-authority";
import * as React from "react";
import { useTranslation } from "react-i18next";

export type StatusTone = "ok" | "warn" | "bad" | "running" | "neutral";

// Status ink on its own soft background (Spec 8); the ring keeps the pill's edge in both themes.
const pillVariants = cva(
	"inline-flex w-fit shrink-0 items-center gap-1.5 rounded-full px-2 py-0.5 text-xs leading-4 font-medium whitespace-nowrap ring-1 ring-inset [&>svg]:size-3",
	{
		variants: {
			tone: {
				ok: "bg-success-soft text-success-ink ring-success/25",
				warn: "bg-warning-soft text-warning-ink ring-warning/30",
				bad: "bg-danger-soft text-danger-ink ring-danger/25",
				running: "bg-info-soft text-info-ink ring-info/25",
				neutral: "bg-muted text-muted-foreground ring-border",
			},
		},
		defaultVariants: { tone: "neutral" },
	},
);

const dotTone: Record<StatusTone, string> = {
	ok: "bg-success",
	warn: "bg-warning",
	bad: "bg-danger",
	running: "bg-info",
	neutral: "bg-unmeasured",
};

/** A small colored dot; pulses when `pulse` is set (defaults on for running). */
export function StatusDot({ tone = "neutral", pulse, className }: { tone?: StatusTone; pulse?: boolean; className?: string }) {
	const live = pulse ?? tone === "running";
	return (
		<span data-slot="status-dot" aria-hidden="true" className={cn("relative inline-flex size-2 shrink-0", className)}>
			{live && <span className={cn("absolute inset-0 animate-ping rounded-full opacity-60 motion-reduce:hidden", dotTone[tone])} />}
			<span className={cn("relative inline-flex size-2 rounded-full", dotTone[tone])} />
		</span>
	);
}

/**
 * The one pulsing "live" dot (X-10): something is running right now. Use it instead of a hand-made
 * animate-ping span. `tone` defaults to running (blue); `size` is the dot's Tailwind size class.
 */
export function LiveDot({ tone = "running", size = "size-2", className, label }: { tone?: StatusTone; size?: string; className?: string; label?: string }) {
	return (
		<span
			data-slot="live-dot"
			role={label ? "img" : undefined}
			aria-label={label}
			aria-hidden={label ? undefined : true}
			className={cn("relative inline-flex shrink-0", size, className)}
		>
			<span className={cn("absolute inset-0 animate-ping rounded-full opacity-60 motion-reduce:hidden", dotTone[tone])} />
			<span className={cn("relative inline-flex rounded-full", size, dotTone[tone])} />
		</span>
	);
}

export interface StatusPillProps extends Omit<React.ComponentProps<"span">, "children"> {
	tone?: StatusTone;
	/**
	 * A raw server status ("cancelled", "budget_stop", "awaiting_approval"). When set, the pill takes
	 * its word, tone and icon from the shared vocabulary (lib/status/vocabulary.ts) and children are
	 * optional (they replace the word).
	 */
	raw?: string | null;
	children?: React.ReactNode;
	/** Leading dot. Default true. Set false when passing an icon as a child. Ignored with `raw` (it shows the icon). */
	dot?: boolean;
	/** Pulse the dot. Defaults to true for tone "running". */
	pulse?: boolean;
	testId?: string;
}

/** Status as color + icon or dot + one short word ("Finished", "Failed", "Running"). */
export function StatusPill({ tone, raw, dot = true, pulse, className, children, testId, ...props }: StatusPillProps) {
	const { t } = useTranslation("status");
	if (raw !== undefined) {
		const info = statusOf(raw);
		const Icon = info.icon;
		const finalTone = tone ?? info.tone;
		return (
			<span
				data-slot="status-pill"
				data-tone={finalTone}
				data-state={info.state}
				data-testid={testId}
				className={cn(pillVariants({ tone: finalTone }), className)}
				{...props}
			>
				<Icon aria-hidden="true" className={cn(info.state === "running" && "animate-spin motion-reduce:animate-none [animation-duration:2s]")} />
				{children ?? t(info.labelKey.slice("status:".length) as never)}
			</span>
		);
	}
	const finalTone = tone ?? "neutral";
	return (
		<span data-slot="status-pill" data-tone={finalTone} data-testid={testId} className={cn(pillVariants({ tone: finalTone }), className)} {...props}>
			{dot && <StatusDot tone={finalTone} pulse={pulse} />}
			{children}
		</span>
	);
}
