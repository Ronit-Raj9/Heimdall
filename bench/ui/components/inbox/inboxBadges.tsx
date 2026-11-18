import { Badge } from "@/components/ui/badge";
import { formatDateTime } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { InboxReleaseStatus } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { ExternalLink } from "lucide-react";
import { useTranslation } from "react-i18next";
import { isInboxState, isInboxTaskKind, releaseSummary, releaseVariant, safeExternalHref, stateVariant } from "./inbox.utils";

/** An RFC 3339 time in the reader's locale, or the raw text when it does not parse. */
export function formatInboxTime(iso: string | undefined): string {
	if (!iso) return "";
	return formatDateTime(iso, getLocale()) ?? iso;
}

/** One item state as a badge. A state this UI does not know yet shows as the server sent it. */
export function InboxStateBadge({ state, testId }: { state: string; testId?: string }) {
	const { t } = useTranslation("bench");
	return (
		<Badge variant={stateVariant(state)} data-state={state} data-testid={testId}>
			{isInboxState(state) ? t(`inbox.state.${state}`) : state}
		</Badge>
	);
}

/** A task kind's label; an unknown kind shows raw. */
export function useKindLabel(): (kind: string) => string {
	const { t } = useTranslation("bench");
	return (kind) => (isInboxTaskKind(kind) ? t(`inbox.kind.${kind}`) : kind);
}

/** The text of a regression task's newest result: "passes in e_123 (3/3)", "fails ...", "not run yet". */
export function useReleaseText(): (release: InboxReleaseStatus | undefined) => string {
	const { t } = useTranslation("bench");
	return (release) => {
		const summary = releaseSummary(release);
		switch (summary.key) {
			case "inbox.release.passes":
			case "inbox.release.fails":
				return t(summary.key, summary.values);
			case "inbox.release.passesNoExp":
			case "inbox.release.failsNoExp":
				return t(summary.key, summary.values);
			case "inbox.release.notRun":
				return t(summary.key);
			default:
				return t(summary.key, summary.values);
		}
	};
}

/** A regression task's newest result as a badge. */
export function ReleaseBadge({ release, testId }: { release: InboxReleaseStatus | undefined; testId?: string }) {
	const text = useReleaseText();
	return (
		<Badge variant={releaseVariant(release)} data-status={release?.status ?? "not_run"} data-testid={testId}>
			{text(release)}
		</Badge>
	);
}

/**
 * A link to a trace in its source's own UI, opened in a new tab. Only http and https links render;
 * anything else renders nothing, since the link comes from outside.
 */
export function TraceSourceLink({
	link,
	traceId,
	source,
	label,
	testId,
	className,
}: {
	link: string | undefined;
	traceId: string;
	source: string;
	label: string;
	testId?: string;
	className?: string;
}) {
	const { t } = useTranslation("bench");
	const href = safeExternalHref(link);
	if (!href) return null;
	return (
		<a
			href={href}
			target="_blank"
			rel="noopener noreferrer"
			aria-label={t("inbox.table.openTraceAria", { id: traceId, source })}
			className={cn(
				"text-primary focus-visible:ring-ring/50 inline-flex w-fit items-center gap-1 rounded-sm text-xs font-medium underline-offset-2 hover:underline focus-visible:ring-[3px] focus-visible:outline-none",
				className,
			)}
			data-testid={testId}
		>
			<ExternalLink className="size-3" aria-hidden="true" />
			{label}
		</a>
	);
}