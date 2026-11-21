import { ForbiddenNote, isRoleRefusal } from "@/components/platform/forbiddenNote";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { cn } from "@/lib/utils";
import { AlertTriangle } from "lucide-react";
import { useTranslation } from "react-i18next";

export interface LoadErrorProps {
	title: string;
	error: unknown;
	onRetry?: () => void;
	/** data-testid of the block; the retry button gets "<testId>-retry". */
	testId: string;
	className?: string;
}

/** A failed request: what failed, the server's message, and a retry. Used by the runs, receipts and new-experiment screens. */
export function LoadError({ title, error, onRetry, testId, className }: LoadErrorProps) {
	const { t } = useTranslation("bench");
	const session = useSession();
	// Hosted mode: a 403 is the caller's role, not a fault; say so instead of offering a retry.
	if (isRoleRefusal(session, error)) return <ForbiddenNote error={error} testId={testId} className={className} />;
	return (
		<div
			role="alert"
			data-testid={testId}
			className={cn(
				"border-chart-error/30 bg-chart-error/5 flex flex-wrap items-start gap-3 rounded-xl border px-4 py-3.5 text-sm sm:flex-nowrap",
				className,
			)}
		>
			<span className="bg-chart-error/10 flex size-7 shrink-0 items-center justify-center rounded-full">
				<AlertTriangle className="text-chart-error-ink size-3.5" aria-hidden="true" />
			</span>
			<div className="flex min-w-0 flex-1 flex-col gap-1">
				<p className="text-foreground font-medium">{title}</p>
				<p className="text-muted-foreground line-clamp-3 break-words" title={getBenchErrorMessage(error)}>
					{getBenchErrorMessage(error)}
				</p>
			</div>
			{onRetry && (
				<Button variant="outline" size="sm" onClick={onRetry} dataTestId={`${testId}-retry`}>
					{t("runs.state.retry")}
				</Button>
			)}
		</div>
	);
}

/** An honest empty state: says what is missing, not a blank box. */
export function EmptyNote({ children, testId, className }: { children: React.ReactNode; testId: string; className?: string }) {
	return (
		<p
			data-testid={testId}
			className={cn(
				"text-muted-foreground bg-muted/20 rounded-xl border border-dashed border-border/80 px-6 py-10 text-center text-sm text-pretty",
				className,
			)}
		>
			{children}
		</p>
	);
}

/** Placeholder rows while a section loads. */
export function BlockSkeleton({ rows = 3, testId, className }: { rows?: number; testId: string; className?: string }) {
	const { t } = useTranslation("bench");
	return (
		<div className={cn("flex flex-col gap-2.5", className)} data-testid={testId} aria-busy="true" aria-label={t("runs.state.loading")}>
			{Array.from({ length: rows }, (_, i) => (
				<Skeleton key={`skeleton-${i}`} className="h-10 w-full rounded-lg" style={{ opacity: Math.max(0.4, 1 - i * 0.12) }} />
			))}
		</div>
	);
}

/** Section frame shared by the three screens: heading, one-line description, body. */
export function Section({
	title,
	description,
	children,
	testId,
	actions,
	className,
}: {
	title: string;
	description?: string;
	children: React.ReactNode;
	testId: string;
	actions?: React.ReactNode;
	className?: string;
}) {
	return (
		<section className={cn("flex flex-col gap-3", className)} data-testid={testId}>
			<div className="flex flex-wrap items-end justify-between gap-2">
				<div className="flex flex-col gap-0.5">
					<h2 className="text-foreground text-base font-semibold tracking-tight">{title}</h2>
					{description && <p className="text-muted-foreground line-clamp-2 max-w-prose text-sm">{description}</p>}
				</div>
				{actions}
			</div>
			{children}
		</section>
	);
}