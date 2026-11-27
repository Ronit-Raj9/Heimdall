import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { JobStatus, Role, RunnerState } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import "@/components/share/uf8bI18n";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

const ROLE_CLASS: Record<Role, string> = {
	// Neutral, not the primary button colour (X-61); the weight tells it apart.
	owner: "border-border bg-muted text-foreground font-semibold",
	admin: "border-chart-cat-2/45 bg-chart-cat-2/10 text-foreground",
	member: "border-border bg-secondary text-secondary-foreground",
	viewer: "border-border text-muted-foreground",
};

/** A membership role as a small badge. */
export function RoleBadge({ role, className }: { role: Role; className?: string }) {
	const { t } = useTranslation("platform");
	return (
		<Badge variant="outline" className={cn("font-medium", ROLE_CLASS[role], className)} data-role={role}>
			{t(`roles.${role}`)}
		</Badge>
	);
}

const JOB_VARIANT: Record<JobStatus, "default" | "success" | "destructive" | "warning" | "outline" | "secondary"> = {
	queued: "outline",
	running: "default",
	succeeded: "success",
	failed: "destructive",
	// A grey filled pill: an outline one read as a disabled control (X-63).
	cancelled: "secondary",
};

// A dot per status, shaped so the badge reads without colour.
const JOB_DOT: Record<JobStatus, string> = {
	queued: "rounded-full border-[1.5px] border-current",
	running: "rounded-full bg-current animate-pulse",
	succeeded: "rounded-full bg-current",
	failed: "rotate-45 rounded-[1px] bg-current",
	cancelled: "h-[1.5px] w-2 bg-current",
};

/** A job's status as a badge. */
export function JobStatusBadge({ status, className }: { status: JobStatus; className?: string }) {
	const { t } = useTranslation("platform");
	const { t: tu } = useTranslation("uf8b");
	return (
		<Badge
			variant={JOB_VARIANT[status]}
			className={cn("gap-1.5", className)}
			title={tu(`jobs.statusTitle.${status}`)}
			data-status={status}
			data-testid={`job-status-${status}`}
		>
			<span aria-hidden="true" className={cn("inline-block size-1.5 shrink-0", JOB_DOT[status])} />
			{t(`jobs.statuses.${status}`)}
		</Badge>
	);
}

const RUNNER_VARIANT: Record<RunnerState, "success" | "default" | "outline"> = {
	idle: "success",
	running: "default",
	lost: "outline",
};

/** A runner's state as a badge. Unknown states show as written. */
export function RunnerStatusBadge({ state }: { state: string }) {
	const { t } = useTranslation("platform");
	const known = state in RUNNER_VARIANT ? (state as RunnerState) : null;
	return (
		<Badge
			variant={known ? RUNNER_VARIANT[known] : "outline"}
			className={cn("gap-1.5", known === "lost" && "text-muted-foreground")}
			data-testid={`runner-state-${state}`}
		>
			<span
				aria-hidden="true"
				className={cn(
					"inline-block size-1.5 rounded-full",
					known === "lost" ? "border-[1.5px] border-current" : "bg-current",
					known === "running" && "animate-pulse",
				)}
			/>
			{known ? t(`settings.runners.statuses.${known}`) : state}
		</Badge>
	);
}

export interface ConfirmButtonProps {
	/** The button that asks first. */
	label: ReactNode;
	/** The question shown in place of the button. */
	question: string;
	confirmLabel: string;
	cancelLabel: string;
	onConfirm: () => void;
	isLoading?: boolean;
	disabled?: boolean;
	variant?: "outline" | "destructive" | "ghost";
	testId: string;
	className?: string;
}

/** How long an armed ConfirmButton waits before it disarms by itself. */
export const CONFIRM_DISARM_MS = 5000;

/**
 * A destructive action that asks inline before it acts: the button turns into the question with
 * confirm and cancel, so there is no modal to manage. Armed, focus moves to the confirm button;
 * Escape, a click or focus outside, or 5 seconds of nothing disarm it and focus returns to the
 * trigger (X-105).
 */
export function ConfirmButton({
	label,
	question,
	confirmLabel,
	cancelLabel,
	onConfirm,
	isLoading,
	disabled,
	variant = "outline",
	testId,
	className,
}: ConfirmButtonProps) {
	const [asking, setAsking] = useState(false);
	const groupRef = useRef<HTMLSpanElement>(null);
	const triggerRef = useRef<HTMLButtonElement>(null);
	const refocus = useRef(false);

	const disarm = (focusTrigger: boolean) => {
		refocus.current = focusTrigger;
		setAsking(false);
	};

	useEffect(() => {
		if (!asking && refocus.current) {
			refocus.current = false;
			triggerRef.current?.focus();
		}
		if (!asking) return;
		const timer = window.setTimeout(() => disarm(false), CONFIRM_DISARM_MS);
		const onKey = (e: KeyboardEvent) => {
			if (e.key === "Escape") {
				e.stopPropagation();
				disarm(true);
			}
		};
		const onDown = (e: PointerEvent) => {
			if (groupRef.current && !groupRef.current.contains(e.target as Node)) disarm(false);
		};
		document.addEventListener("keydown", onKey, true);
		document.addEventListener("pointerdown", onDown, true);
		return () => {
			window.clearTimeout(timer);
			document.removeEventListener("keydown", onKey, true);
			document.removeEventListener("pointerdown", onDown, true);
		};
	}, [asking]);

	if (!asking) {
		return (
			<Button
				ref={triggerRef}
				type="button"
				variant={variant}
				size="sm"
				className={cn("h-7 px-2.5 text-xs", className)}
				disabled={disabled}
				isLoading={isLoading}
				onClick={() => setAsking(true)}
				dataTestId={testId}
			>
				{label}
			</Button>
		);
	}
	return (
		<span
			ref={groupRef}
			className="inline-flex flex-wrap items-center justify-end gap-2"
			onBlur={(e) => {
				// Focus leaving the question (Tab away) disarms it.
				if (e.relatedTarget && !e.currentTarget.contains(e.relatedTarget as Node)) disarm(false);
			}}
			role="group"
			aria-label={question}
			data-testid={`${testId}-confirm`}
		>
			<span className="text-foreground max-w-80 text-xs whitespace-normal">{question}</span>
			<Button
				type="button"
				variant="destructive"
				size="sm"
				className="h-7 px-2.5 text-xs"
				autoFocus
				onClick={() => {
					setAsking(false);
					onConfirm();
				}}
				dataTestId={`${testId}-yes`}
			>
				{confirmLabel}
			</Button>
			<Button
				type="button"
				variant="ghost"
				size="sm"
				className="h-7 px-2.5 text-xs"
				onClick={() => disarm(true)}
				dataTestId={`${testId}-no`}
			>
				{cancelLabel}
			</Button>
		</span>
	);
}