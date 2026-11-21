import { cn } from "@/lib/utils";
import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

/** Status tones shared by the operate, admin, usage, workers and jobs screens. */
export type Tone = "good" | "warn" | "bad" | "live" | "idle";

const DOT: Record<Tone, string> = {
	good: "bg-chart-success",
	warn: "bg-chart-warning",
	bad: "bg-chart-error",
	live: "bg-primary",
	idle: "bg-muted-foreground/50",
};

const PILL: Record<Tone, string> = {
	good: "bg-chart-success/10 text-chart-success-ink",
	warn: "bg-chart-warning/10 text-chart-warning-ink",
	bad: "bg-chart-error/10 text-chart-error-ink",
	live: "bg-primary/10 text-primary",
	idle: "bg-muted text-muted-foreground",
};

/** A small colored dot; pulses when the thing is live. */
export function StatusDot({ tone, pulse, className }: { tone: Tone; pulse?: boolean; className?: string }) {
	return (
		<span className={cn("relative inline-flex size-2 shrink-0", className)} aria-hidden="true">
			{pulse && <span className={cn("absolute inline-flex size-full animate-ping rounded-full opacity-60", DOT[tone])} />}
			<span className={cn("relative inline-flex size-2 rounded-full", DOT[tone])} />
		</span>
	);
}

/** A soft rounded pill with a dot and a short word. */
export function TonePill({ tone, pulse, children, testId }: { tone: Tone; pulse?: boolean; children: ReactNode; testId?: string }) {
	return (
		<span
			className={cn("inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap", PILL[tone])}
			data-testid={testId}
		>
			<StatusDot tone={tone} pulse={pulse} />
			{children}
		</span>
	);
}

/** A headline number with a label and an icon. */
export function StatCard({
	label,
	value,
	icon: Icon,
	hint,
	testId,
	valueTestId,
}: {
	label: string;
	value: ReactNode;
	icon?: LucideIcon;
	hint?: ReactNode;
	testId?: string;
	valueTestId?: string;
}) {
	return (
		<div className="bg-card flex min-w-0 flex-col gap-2 rounded-xl border p-5 transition-shadow hover:shadow-md" data-testid={testId}>
			<div className="text-muted-foreground flex items-center justify-between gap-2 text-sm">
				<span className="truncate">{label}</span>
				{Icon && (
					<span className="bg-muted flex size-8 shrink-0 items-center justify-center rounded-lg">
						<Icon className="size-4" aria-hidden="true" />
					</span>
				)}
			</div>
			<div className="text-foreground truncate text-2xl font-semibold tracking-tight tabular-nums" data-testid={valueTestId}>
				{value}
			</div>
			{hint && <div className="text-muted-foreground truncate text-xs">{hint}</div>}
		</div>
	);
}

/** A health tile: icon, name, status pill and one short line. */
export function HealthTile({
	icon: Icon,
	title,
	tone,
	status,
	line,
	pulse,
	testId,
}: {
	icon: LucideIcon;
	title: string;
	tone: Tone;
	status: string;
	line?: ReactNode;
	pulse?: boolean;
	testId?: string;
}) {
	return (
		<div className="bg-card flex min-w-0 flex-col gap-3 rounded-xl border p-5 transition-shadow hover:shadow-md" data-testid={testId} data-tone={tone}>
			<div className="flex items-center justify-between gap-2">
				<span className="bg-muted text-foreground flex size-9 items-center justify-center rounded-lg">
					<Icon className="size-4" aria-hidden="true" />
				</span>
				<TonePill tone={tone} pulse={pulse}>
					{status}
				</TonePill>
			</div>
			<div className="min-w-0">
				<p className="text-foreground text-base font-semibold">{title}</p>
				{line && <p className="text-muted-foreground line-clamp-2 text-sm">{line}</p>}
			</div>
		</div>
	);
}

/** A titled section card with an optional icon and right-side slot. */
export function Panel({
	title,
	description,
	icon: Icon,
	aside,
	children,
	className,
	testId,
}: {
	title: string;
	description?: string;
	icon?: LucideIcon;
	aside?: ReactNode;
	children: ReactNode;
	className?: string;
	testId?: string;
}) {
	return (
		<section className={cn("bg-card flex min-w-0 flex-col gap-4 rounded-xl border p-5", className)} data-testid={testId}>
			<header className="flex flex-wrap items-start justify-between gap-3">
				<div className="flex min-w-0 items-start gap-3">
					{Icon && (
						<span className="bg-muted flex size-8 shrink-0 items-center justify-center rounded-lg">
							<Icon className="size-4" aria-hidden="true" />
						</span>
					)}
					<div className="min-w-0">
						<h2 className="text-foreground text-base font-semibold">{title}</h2>
						{description && <p className="text-muted-foreground line-clamp-2 text-sm">{description}</p>}
					</div>
				</div>
				{aside}
			</header>
			{children}
		</section>
	);
}
