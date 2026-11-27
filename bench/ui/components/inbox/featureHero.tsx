import { cn } from "@/lib/utils";
import { ChevronDown, type LucideIcon } from "lucide-react";
import { useState, type ReactNode } from "react";

/** The first thing a setup page shows: a small icon beside the title, one sentence and one primary action. Not clickable itself, so no dashed edge and no hover lift (X-49). */
export function FeatureHero({
	icon: Icon,
	title,
	message,
	action,
	children,
	testId,
	className,
}: {
	icon: LucideIcon;
	title: ReactNode;
	message: ReactNode;
	action?: ReactNode;
	/** Extra muted detail under the action (a command, a doc pointer). */
	children?: ReactNode;
	testId?: string;
	className?: string;
}) {
	return (
		<div
			className={cn("bg-card flex flex-col items-center gap-4 rounded-xl border px-6 py-10 text-center", className)}
			data-testid={testId}
		>
			<div className="flex max-w-md flex-col gap-1">
				<h2 className="text-foreground flex items-center justify-center gap-2 text-lg font-semibold tracking-tight">
					<Icon className="text-muted-foreground size-5 shrink-0" aria-hidden="true" />
					{title}
				</h2>
				<p className="text-muted-foreground text-sm">{message}</p>
			</div>
			{action}
			{children && <div className="text-muted-foreground flex max-w-md flex-col items-center gap-2 text-sm">{children}</div>}
		</div>
	);
}

/** A rounded card with a title row; the body holds a stepped setup form. */
export function SetupCard({
	icon: Icon,
	title,
	description,
	actions,
	children,
	testId,
	id,
}: {
	icon?: LucideIcon;
	title: ReactNode;
	description?: ReactNode;
	actions?: ReactNode;
	children: ReactNode;
	testId?: string;
	id?: string;
}) {
	return (
		<section id={id} className="bg-card flex scroll-mt-4 flex-col gap-5 rounded-xl border p-5 shadow-xs sm:p-6" data-testid={testId}>
			<div className="flex flex-wrap items-start justify-between gap-3">
				<div className="flex items-start gap-3">
					{Icon && (
						<span className="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-full">
							<Icon className="size-4.5" aria-hidden="true" />
						</span>
					)}
					<div className="flex flex-col gap-0.5">
						<h2 className="text-foreground text-base font-semibold tracking-tight">{title}</h2>
						{description && <p className="text-muted-foreground line-clamp-2 max-w-prose text-sm">{description}</p>}
					</div>
				</div>
				{actions}
			</div>
			{children}
		</section>
	);
}

/** One numbered step of a setup form. */
export function Step({ n, title, children, last }: { n: number; title: ReactNode; children: ReactNode; last?: boolean }) {
	return (
		<div className="flex gap-4">
			<div className="flex flex-col items-center">
				<span className="bg-primary text-primary-foreground flex size-6 shrink-0 items-center justify-center rounded-full text-xs font-semibold tabular-nums">
					{n}
				</span>
				{!last && <span className="bg-border mt-1 w-px flex-1" aria-hidden="true" />}
			</div>
			<div className={cn("flex min-w-0 flex-1 flex-col gap-3", !last && "pb-5")}>
				<h3 className="text-foreground text-sm font-medium">{title}</h3>
				{children}
			</div>
		</div>
	);
}

/**
 * A collapsed section (native details, so content stays in the markup): webhooks, tokens, alerts.
 * While closed its body is inert, so nothing inside is a tab stop (M7-17). Inner blocks sit on
 * dividers, not in more cards (M7-18).
 */
export function Collapsible({
	icon: Icon,
	title,
	hint,
	children,
	defaultOpen,
	testId,
	id,
}: {
	icon?: LucideIcon;
	title: ReactNode;
	hint?: ReactNode;
	children: ReactNode;
	defaultOpen?: boolean;
	testId?: string;
	id?: string;
}) {
	const [open, setOpen] = useState(Boolean(defaultOpen));
	return (
		<details
			id={id}
			open={defaultOpen}
			onToggle={(e) => setOpen(e.currentTarget.open)}
			className="group bg-card scroll-mt-4 rounded-xl border"
			data-testid={testId}
		>
			<summary className="hover:bg-muted/40 flex cursor-pointer list-none items-center gap-3 rounded-xl px-5 py-4 [&::-webkit-details-marker]:hidden">
				{Icon && <Icon className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />}
				<span className="flex min-w-0 flex-1 flex-col">
					<span className="text-foreground text-sm font-semibold">{title}</span>
					{hint && <span className="text-muted-foreground truncate text-sm">{hint}</span>}
				</span>
				<ChevronDown className="text-muted-foreground size-4 shrink-0 transition-transform group-open:rotate-180" aria-hidden="true" />
			</summary>
			<div inert={!open} className="flex flex-col divide-y border-t px-5 [&>*]:py-5">
				{children}
			</div>
		</details>
	);
}

export type PillTone = "ok" | "warn" | "error" | "running" | "muted";

const PILL: Record<PillTone, { box: string; dot: string }> = {
	// Status tokens, not palette classes, so both themes keep their contrast (X-67).
	ok: { box: "bg-chart-success/10 text-chart-success-ink", dot: "bg-chart-success" },
	warn: { box: "bg-chart-warning/10 text-chart-warning-ink", dot: "bg-chart-warning" },
	error: { box: "bg-chart-error/10 text-chart-error-ink", dot: "bg-chart-error" },
	running: { box: "bg-chart-info/10 text-chart-info-ink", dot: "bg-chart-info animate-pulse" },
	muted: { box: "bg-muted text-muted-foreground", dot: "bg-muted-foreground" },
};

/** A small soft status pill with a colored dot. */
export function Pill({ tone, children, testId, title }: { tone: PillTone; children: ReactNode; testId?: string; title?: string }) {
	const s = PILL[tone];
	return (
		<span
			className={cn("inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap", s.box)}
			data-testid={testId}
			title={title}
		>
			<span className={cn("size-1.5 rounded-full", s.dot)} aria-hidden="true" />
			{children}
		</span>
	);
}

/** Opens every collapsed section around the element with this id, then scrolls to it. */
export function revealAnchor(id: string) {
	if (typeof document === "undefined") return;
	const el = document.getElementById(id);
	if (!el) return;
	for (let p: HTMLElement | null = el; p; p = p.parentElement) if (p instanceof HTMLDetailsElement) p.open = true;
	el.scrollIntoView({ behavior: "smooth", block: "start" });
}