import { ForbiddenNote, isRoleRefusal } from "@/components/platform/forbiddenNote";
import { Button } from "@/components/ui/button";
import { DetailsDisclosure } from "@/components/ui/detailsDisclosure";
import { Skeleton } from "@/components/ui/skeleton";
import { errorKind, kindRetryable } from "@/lib/errorKind";
import { useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { isNotFoundError } from "@/lib/store/apis/notFound";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { AlertTriangle } from "lucide-react";
import { useEffect, useState, type ElementType, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import "./screenStatesI18n";

export interface LoadErrorProps {
	title: string;
	error: unknown;
	onRetry?: () => void;
	/** data-testid of the block; the retry button gets "<testId>-retry". */
	testId: string;
	className?: string;
	/**
	 * What to say when the error is a 404: the thing does not exist, so a retry cannot help. Given,
	 * a 404 shows this text and a link back instead of the server message and Retry. Absent, a 404
	 * renders like any other error.
	 */
	notFound?: LoadErrorNotFound;
}

/** The 404 branch of LoadError: a plain sentence and where to go instead. */
export interface LoadErrorNotFound {
	text: string;
	backTo: string;
	backLabel: string;
}

/** A failed request: what failed, the server's message, and a retry. Used by the runs, receipts and new-experiment screens. */
export function LoadError({ title, error, onRetry, testId, className, notFound }: LoadErrorProps) {
	const { t } = useTranslation("bench");
	const { t: t2 } = useTranslation("ufs2");
	const session = useSession();
	// Hosted mode: a 403 is the caller's role, not a fault; say so instead of offering a retry.
	if (isRoleRefusal(session, error)) return <ForbiddenNote error={error} testId={testId} className={className} />;
	if (notFound && isNotFoundError(error)) {
		return (
			<div
				role="alert"
				data-testid={testId}
				data-not-found="true"
				className={cn("bg-muted/20 flex flex-wrap items-center gap-3 rounded-xl border px-4 py-3.5 text-sm sm:flex-nowrap", className)}
			>
				<div className="flex min-w-0 flex-1 flex-col gap-0.5">
					<p className="text-foreground font-medium">{notFound.text}</p>
					{session.authMode === "hosted" && (
						<p className="text-muted-foreground" data-testid={`${testId}-other-org`}>
							{t2("errors.otherOrg")}
						</p>
					)}
				</div>
				<Button asChild variant="outline" size="sm">
					<Link to={notFound.backTo} data-testid={`${testId}-back`}>
						{notFound.backLabel}
					</Link>
				</Button>
			</div>
		);
	}
	return <ErrorBlock title={title} error={error} onRetry={onRetry} testId={testId} className={className} retryLabel={t("runs.state.retry")} />;
}

/**
 * The error pattern (Spec 5): icon, What (bold), Why (one plain sentence from the kind of error,
 * never a raw code), Retry only when a retry can help, and the server's own text in Details.
 */
function ErrorBlock({
	title,
	error,
	onRetry,
	testId,
	className,
	retryLabel,
	compact,
}: {
	title?: string;
	error: unknown;
	onRetry?: () => void;
	testId: string;
	className?: string;
	retryLabel: string;
	compact?: boolean;
}) {
	const { t } = useTranslation("ufs2");
	const session = useSession();
	const raw = errorKind(error);
	// Local mode has no roles: a 403 there is the server refusing the change (a recorded server).
	const kind = raw === "forbidden" && session.authMode !== "hosted" ? "refused" : raw;
	const server = getBenchErrorMessage(error);
	const what = title ?? t(`errors.${kind}.what` as never);
	const why = t(`errors.${kind}.why` as never);
	return (
		<div
			role="alert"
			data-testid={testId}
			data-error-kind={kind}
			className={cn(
				"border-chart-error/30 bg-chart-error/5 flex flex-wrap items-start gap-3 rounded-xl border text-sm sm:flex-nowrap",
				compact ? "px-3 py-2.5" : "px-4 py-3.5",
				className,
			)}
		>
			<span className="bg-chart-error/10 flex size-7 shrink-0 items-center justify-center rounded-full">
				<AlertTriangle className="text-chart-error-ink size-3.5" aria-hidden="true" />
			</span>
			<div className="flex min-w-0 flex-1 flex-col gap-1">
				<p className="text-foreground font-medium">{what}</p>
				<p className="text-muted-foreground break-words">{title ? `${t(`errors.${kind}.what` as never)} ${why}` : why}</p>
				{server && (
					<DetailsDisclosure label={t("errors.details")} className="text-xs" contentClassName="mt-1">
						<p className="text-muted-foreground font-mono text-xs break-words whitespace-pre-wrap">{server}</p>
					</DetailsDisclosure>
				)}
			</div>
			{onRetry && kind !== "refused" && kindRetryable(kind) && (
				<Button variant="outline" size="sm" onClick={onRetry} dataTestId={`${testId}-retry`}>
					{retryLabel}
				</Button>
			)}
		</div>
	);
}

/**
 * A failed action, shown next to the button that started it (never a toast). Typed input stays;
 * the caller keeps the error in state: try { await trigger(args).unwrap() } catch (e) { setError(e) }.
 */
export function InlineError({
	error,
	onRetry,
	title,
	testId,
	className,
}: {
	/** The caught error; nothing renders while null or undefined. */
	error: unknown;
	onRetry?: () => void;
	/** What failed, when the kind's own sentence is not enough ("Could not stop the run."). */
	title?: string;
	testId: string;
	className?: string;
}) {
	const { t } = useTranslation("bench");
	if (error === null || error === undefined) return null;
	return <ErrorBlock compact title={title} error={error} onRetry={onRetry} testId={testId} className={className} retryLabel={t("runs.state.retry")} />;
}

/**
 * An honest empty state: says what is missing, not a blank box. New code uses EmptyState
 * (`@/components/shell/emptyState`), which takes a title and an action; this stays for the
 * existing sentence-only uses and renders the same frame.
 */
export function EmptyNote({
	children,
	testId,
	className,
	action,
}: {
	children: ReactNode;
	testId: string;
	className?: string;
	/** One next step under the sentence (a Button or Link). */
	action?: ReactNode;
}) {
	if (action) {
		return (
			<div
				data-testid={testId}
				className={cn(
					"text-muted-foreground bg-muted/20 flex flex-col items-center gap-3 rounded-xl border border-dashed border-border/80 px-6 py-10 text-center text-sm text-pretty",
					className,
				)}
			>
				<p>{children}</p>
				{action}
			</div>
		);
	}
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

/** Which message a slow load shows after `ms`: none, "still loading" from 3 s, the error pattern from 15 s. */
export function slowStage(ms: number): "none" | "slow" | "tooSlow" {
	if (ms >= 15000) return "tooSlow";
	if (ms >= 3000) return "slow";
	return "none";
}

function useElapsedStage(): "none" | "slow" | "tooSlow" {
	const [stage, setStage] = useState<"none" | "slow" | "tooSlow">("none");
	useEffect(() => {
		const a = window.setTimeout(() => setStage("slow"), 3000);
		const b = window.setTimeout(() => setStage("tooSlow"), 15000);
		return () => {
			window.clearTimeout(a);
			window.clearTimeout(b);
		};
	}, []);
	return stage;
}

/**
 * Placeholder rows while a section loads. With `what` ("the runs"), it names the load after 3 s
 * ("Still loading the runs...") and after 15 s shows the error pattern with Retry (when given).
 */
export function BlockSkeleton({
	rows = 3,
	testId,
	className,
	what,
	onRetry,
}: {
	rows?: number;
	testId: string;
	className?: string;
	what?: string;
	onRetry?: () => void;
}) {
	const { t } = useTranslation("bench");
	const { t: t2 } = useTranslation("ufs2");
	const stage = useElapsedStage();
	if (stage === "tooSlow") {
		return (
			<div role="alert" data-testid={`${testId}-slow`} className={cn("bg-muted/20 flex flex-wrap items-center gap-3 rounded-xl border px-4 py-3.5 text-sm", className)}>
				<div className="flex min-w-0 flex-1 flex-col gap-0.5">
					<p className="text-foreground font-medium">{what ? t2("loading.tooSlow", { what }) : t2("loading.tooSlowAny")}</p>
					<p className="text-muted-foreground">{t2("loading.tooSlowWhy")}</p>
				</div>
				{onRetry && (
					<Button variant="outline" size="sm" onClick={onRetry} dataTestId={`${testId}-retry`}>
						{t("runs.state.retry")}
					</Button>
				)}
			</div>
		);
	}
	return (
		<div className={cn("flex flex-col gap-2.5", className)} data-testid={testId} aria-busy="true" aria-label={t("runs.state.loading")}>
			{Array.from({ length: rows }, (_, i) => (
				<Skeleton key={`skeleton-${i}`} className="h-10 w-full rounded-lg" style={{ opacity: Math.max(0.4, 1 - i * 0.12) }} />
			))}
			{stage === "slow" && (
				<p className="text-muted-foreground text-xs" aria-live="polite" data-testid={`${testId}-still`}>
					{what ? t2("loading.slow", { what }) : t2("loading.slowAny")}
				</p>
			)}
		</div>
	);
}

/** A table while it loads: a header bar and `rows` rows of the table's own height (h-11). */
export function TableSkeleton({ rows = 5, testId = "table-skeleton", className }: { rows?: number; testId?: string; className?: string }) {
	const { t } = useTranslation("bench");
	return (
		<div className={cn("overflow-hidden rounded-xl border", className)} data-testid={testId} aria-busy="true" aria-label={t("runs.state.loading")}>
			<Skeleton className="h-10 w-full rounded-none" />
			{Array.from({ length: rows }, (_, i) => (
				<div key={`row-${i}`} className="flex h-11 items-center gap-4 border-t px-3">
					<Skeleton className="h-4 w-2/5" />
					<Skeleton className="h-5 w-16 rounded-full" />
					<Skeleton className="ml-auto h-4 w-12 max-sm:hidden" />
				</div>
			))}
		</div>
	);
}

const GRID_COLS = { 1: "grid-cols-1", 2: "sm:grid-cols-2", 3: "sm:grid-cols-2 lg:grid-cols-3", 4: "sm:grid-cols-2 lg:grid-cols-4" } as const;

/** A grid of cards while it loads. */
export function CardGridSkeleton({
	count = 6,
	columns = 3,
	testId = "card-grid-skeleton",
	className,
}: {
	count?: number;
	columns?: 1 | 2 | 3 | 4;
	testId?: string;
	className?: string;
}) {
	const { t } = useTranslation("bench");
	return (
		<div className={cn("grid grid-cols-1 gap-4", GRID_COLS[columns], className)} data-testid={testId} aria-busy="true" aria-label={t("runs.state.loading")}>
			{Array.from({ length: count }, (_, i) => (
				<Skeleton key={`card-${i}`} className="h-36 w-full rounded-xl" />
			))}
		</div>
	);
}

/** A detail page while it loads: title, a row of stat tiles, a body block. */
export function DetailSkeleton({ testId = "detail-skeleton", className }: { testId?: string; className?: string }) {
	const { t } = useTranslation("bench");
	return (
		<div className={cn("flex flex-col gap-5", className)} data-testid={testId} aria-busy="true" aria-label={t("runs.state.loading")}>
			<Skeleton className="h-7 w-1/3" />
			<div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
				{Array.from({ length: 4 }, (_, i) => (
					<Skeleton key={`tile-${i}`} className="h-20 w-full rounded-xl" />
				))}
			</div>
			<Skeleton className="h-64 w-full rounded-xl" />
		</div>
	);
}

/**
 * Old data stays visible during a refetch, faded and marked busy; never a skeleton again.
 * <Refetching busy={isFetching && !isLoading}>...</Refetching>
 */
export function Refetching({ busy, children, className }: { busy: boolean; children: ReactNode; className?: string }) {
	return (
		<div aria-busy={busy || undefined} className={cn("transition-opacity", busy && "opacity-60", className)}>
			{children}
		</div>
	);
}

/**
 * What a list query shows, in the order that keeps a failure from looking empty: loading, then
 * error, then empty, then ready. Same rule as lib/hooks/useOptionsState.ts.
 */
export function listState(q: { isLoading?: boolean; isError?: boolean; data?: readonly unknown[] | null }): "loading" | "error" | "empty" | "ready" {
	if (q.isLoading) return "loading";
	if (q.isError) return "error";
	if (!q.data || q.data.length === 0) return "empty";
	return "ready";
}

/** Section frame shared by the three screens: heading, one-line description, body. */
export function Section({
	title,
	description,
	children,
	testId,
	actions,
	className,
	as: Heading = "h2",
}: {
	title: string;
	description?: string;
	children: ReactNode;
	testId: string;
	actions?: ReactNode;
	className?: string;
	/** Heading level; pass "h3" inside a tab panel under an h2. */
	as?: ElementType;
}) {
	return (
		<section className={cn("flex flex-col gap-3", className)} data-testid={testId}>
			<div className="flex flex-wrap items-end justify-between gap-2">
				<div className="flex flex-col gap-0.5">
					<Heading className="text-foreground text-base font-semibold tracking-tight">{title}</Heading>
					{description && <p className="text-muted-foreground max-w-prose text-sm">{description}</p>}
				</div>
				{actions}
			</div>
			{children}
		</section>
	);
}