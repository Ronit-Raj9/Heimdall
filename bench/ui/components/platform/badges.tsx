import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { JobStatus, Role, RunnerState } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

const ROLE_CLASS: Record<Role, string> = {
	owner: "border-primary/40 bg-primary/10 text-primary",
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

const JOB_VARIANT: Record<JobStatus, "default" | "success" | "destructive" | "warning" | "outline"> = {
	queued: "outline",
	running: "default",
	succeeded: "success",
	failed: "destructive",
	cancelled: "outline",
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
	return (
		<Badge
			variant={JOB_VARIANT[status]}
			className={cn("gap-1.5", status === "cancelled" && "text-muted-foreground", className)}
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

/**
 * A destructive action that asks inline before it acts: the button turns into the question with
 * confirm and cancel, so there is no modal to manage and focus stays where the user is.
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
	if (!asking) {
		return (
			<Button
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
			className="inline-flex flex-wrap items-center justify-end gap-2"
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
				onClick={() => setAsking(false)}
				dataTestId={`${testId}-no`}
			>
				{cancelLabel}
			</Button>
		</span>
	);
}