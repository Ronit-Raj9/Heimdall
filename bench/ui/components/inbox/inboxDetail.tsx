import { Badge } from "@/components/ui/badge";
import { EntityLink } from "@/components/shell";
import { Button } from "@/components/ui/button";
import type { InboxItem, InboxItemDetail } from "@/lib/types/bench";
import { X } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { redactionEntries } from "./inbox.utils";
import { formatInboxTime, InboxStateBadge, ReleaseBadge, TraceSourceLink, useKindLabel } from "./inboxBadges";

export interface InboxItemDetailViewProps {
	detail: InboxItemDetail;
	/** The accept, reject and snooze block, and the draft after an accept. */
	actions?: ReactNode;
	onClose?: () => void;
}

/**
 * One inbox item: what was seen and why it was flagged, the redacted trace as plain text, its
 * signature and matching traces, what it became, and the actions.
 */
export function InboxItemDetailView({ detail, actions, onClose }: InboxItemDetailViewProps) {
	const { t } = useTranslation("bench");
	const { item } = detail;
	// Go writes an empty list as null; read it as empty.
	const flagged = item.flagged ?? [];
	const traces = item.traces ?? [];
	return (
		<article className="bg-card flex min-w-0 flex-col gap-5 rounded-lg border p-4" data-testid="inbox-detail" data-item-id={item.id}>
			<header className="flex items-start justify-between gap-3">
				<div className="flex min-w-0 flex-col gap-1">
					<div className="flex flex-wrap items-center gap-2">
						<InboxStateBadge state={item.state} testId="inbox-detail-state" />
						<span className="text-muted-foreground font-mono text-xs">{item.id}</span>
					</div>
					<h2 className="text-foreground text-[15px] font-semibold break-words">{item.title}</h2>
					<div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
						<span className="text-muted-foreground font-mono">{t("inbox.detail.trace", { id: item.trace_id })}</span>
						<TraceSourceLink
							link={item.link}
							traceId={item.trace_id}
							source={item.source}
							label={t("inbox.detail.openTrace", { source: item.source })}
							testId="inbox-detail-trace-link"
						/>
					</div>
				</div>
				{onClose && (
					<Button variant="ghost" size="icon" onClick={onClose} aria-label={t("inbox.detail.close")} dataTestId="inbox-detail-close">
						<X aria-hidden="true" />
					</Button>
				)}
			</header>

			<ItemOutcome item={item} />
			<ItemFacts item={item} />

			{flagged.length > 0 && (
				<Block title={t("inbox.detail.flagged")}>
					<ul className="list-disc pl-5 text-sm break-words" data-testid="inbox-detail-flagged">
						{flagged.map((reason, i) => (
							<li key={`${i}-${reason}`}>{reason}</li>
						))}
					</ul>
				</Block>
			)}

			<Block title={t("inbox.detail.preview")} hint={t("inbox.detail.previewHint")}>
				<InboxPreview text={detail.preview} />
				<RedactionNote redacted={item.redacted} />
			</Block>

			<Block title={t("inbox.detail.signature")} hint={t("inbox.detail.signatureHint")}>
				<dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs" data-testid="inbox-detail-signature">
					<dt className="text-muted-foreground">{t("inbox.detail.signature")}</dt>
					<dd className="font-mono break-all">{item.signature}</dd>
					<dt className="text-muted-foreground">{t("inbox.detail.parts.request")}</dt>
					<dd className="font-mono break-all">{item.signature_parts?.request}</dd>
					{item.signature_parts?.tool && (
						<>
							<dt className="text-muted-foreground">{t("inbox.detail.parts.tool")}</dt>
							<dd className="font-mono break-all">{item.signature_parts.tool}</dd>
						</>
					)}
					{item.signature_parts?.error && (
						<>
							<dt className="text-muted-foreground">{t("inbox.detail.parts.error")}</dt>
							<dd className="font-mono break-all">{item.signature_parts.error}</dd>
						</>
					)}
				</dl>
			</Block>

			<Block title={t("inbox.detail.traces")} hint={t("inbox.detail.tracesHint")}>
				<ol className="flex flex-col gap-1 text-xs" data-testid="inbox-detail-traces">
					{traces.map((trace) => (
						<li key={`${trace.source}/${trace.id}`} className="flex flex-wrap items-center gap-x-3 gap-y-0.5">
							<span className="font-mono break-all">{trace.id}</span>
							<span className="text-muted-foreground font-mono">{trace.source}</span>
							<span className="text-muted-foreground">{formatInboxTime(trace.at)}</span>
							<TraceSourceLink
								link={trace.link}
								traceId={trace.id}
								source={trace.source}
								label={t("inbox.table.openTrace")}
								testId={`inbox-detail-trace-${trace.id}`}
							/>
						</li>
					))}
				</ol>
			</Block>

			{actions && (
				<Block title={t("inbox.actions.title")}>
					<div className="flex flex-col gap-4">{actions}</div>
				</Block>
			)}
		</article>
	);
}

/**
 * The redacted trace exactly as stored, as preformatted text. React escapes it, so markup in a
 * trace (a planted script tag, say) shows as text and never runs.
 */
export function InboxPreview({ text }: { text: string }) {
	const { t } = useTranslation("bench");
	if (!text) {
		return (
			<p className="text-muted-foreground text-sm" data-testid="inbox-preview-empty">
				{t("inbox.detail.previewEmpty")}
			</p>
		);
	}
	return (
		<pre
			className="bg-muted/40 max-h-[28rem] overflow-auto rounded-sm border px-3 py-2 font-mono text-xs leading-relaxed break-words whitespace-pre-wrap"
			data-testid="inbox-preview"
		>
			{text}
		</pre>
	);
}

function RedactionNote({ redacted }: { redacted: Record<string, number> | undefined }) {
	const { t } = useTranslation("bench");
	const entries = redactionEntries(redacted);
	if (entries.length === 0) return null;
	const list = entries.map(([kind, n]) => `${n} ${kind}`).join(", ");
	return (
		<p className="text-muted-foreground text-xs" data-testid="inbox-detail-redacted">
			{t("inbox.detail.redacted", { list })}
		</p>
	);
}

/** Facts about the item: source, outcome, model, tools, how often it was seen, what it would become. */
function ItemFacts({ item }: { item: InboxItem }) {
	const { t } = useTranslation("bench");
	const kindLabel = useKindLabel();
	const kind = kindLabel(item.suggested_kind);
	return (
		<dl
			className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs"
			aria-label={t("inbox.detail.facts")}
			data-testid="inbox-detail-facts"
		>
			<dt className="text-muted-foreground">{t("inbox.detail.source")}</dt>
			<dd className="font-mono">
				{item.source} ({item.source_kind})
			</dd>
			<dt className="text-muted-foreground">{t("inbox.detail.outcome")}</dt>
			<dd>
				<Badge variant={item.outcome === "error" ? "destructive" : "outline"}>
					{item.outcome === "error" || item.outcome === "ok" || item.outcome === "unknown"
						? t(`inbox.outcome.${item.outcome}`)
						: item.outcome}
				</Badge>
			</dd>
			{item.model && (
				<>
					<dt className="text-muted-foreground">{t("inbox.detail.model")}</dt>
					<dd className="font-mono">{item.model}</dd>
				</>
			)}
			{item.tools && item.tools.length > 0 && (
				<>
					<dt className="text-muted-foreground">{t("inbox.detail.tools")}</dt>
					<dd className="font-mono break-words">{item.tools.join(", ")}</dd>
				</>
			)}
			<dt className="text-muted-foreground">{t("inbox.detail.seen")}</dt>
			<dd>
				{t("inbox.detail.seenValue", {
					count: item.occurrences,
					first: formatInboxTime(item.first_seen),
					last: formatInboxTime(item.last_seen),
				})}
			</dd>
			<dt className="text-muted-foreground">{t("inbox.detail.suggested")}</dt>
			<dd data-testid="inbox-detail-suggested">
				{item.suggested_suite
					? t("inbox.detail.suggestedValue", { kind, suite: item.suggested_suite })
					: t("inbox.detail.suggestedNoSuite", { kind })}
			</dd>
		</dl>
	);
}

/** What happened to the item: the regression task and how it does, or the reject, duplicate or snooze. */
function ItemOutcome({ item }: { item: InboxItem }) {
	const { t } = useTranslation("bench");
	const notes: ReactNode[] = [];
	if (item.task) {
		const task = item.task;
		notes.push(
			<div key="task" className="flex flex-col gap-1.5" data-testid="inbox-detail-task">
				<p className="text-sm font-medium">
					{t("inbox.detail.taskValue", { suite: task.suite, task: task.id, at: formatInboxTime(task.accepted_at) })}
				</p>
				<p className="text-muted-foreground font-mono text-xs break-all">{t("inbox.detail.taskDir", { dir: task.dir })}</p>
				<div className="flex flex-wrap items-center gap-2 text-xs">
					<Badge variant={task.admitted ? "success" : "warning"} data-testid="inbox-detail-admitted">
						{task.admitted ? t("inbox.detail.admitted") : t("inbox.detail.notAdmitted")}
					</Badge>
					<span className="text-muted-foreground">{t("inbox.detail.release")}</span>
					<ReleaseBadge release={item.release} testId="inbox-detail-release" />
					{item.release?.experiment && <EntityLink kind="experiment" id={item.release.experiment} testId="inbox-detail-release-link" />}
				</div>
				{(item.recurrences ?? 0) > 0 && (
					<p className="text-chart-warning-ink text-xs" data-testid="inbox-detail-recurrences">
						{t("inbox.detail.recurrences", { count: item.recurrences ?? 0, at: formatInboxTime(item.last_recurrence) })}
					</p>
				)}
			</div>,
		);
	}
	if (item.duplicate_of) {
		notes.push(
			<p key="dup" className="text-sm" data-testid="inbox-detail-duplicate">
				{t("inbox.detail.duplicateOf", { id: item.duplicate_of })}
			</p>,
		);
	}
	if (item.reason) {
		notes.push(
			<p key="reason" className="text-sm break-words" data-testid="inbox-detail-reason">
				{t("inbox.detail.rejected", { reason: item.reason })}
			</p>,
		);
	}
	if (item.snoozed_until && item.state === "snoozed") {
		notes.push(
			<p key="snooze" className="text-sm" data-testid="inbox-detail-snoozed">
				{t("inbox.detail.snoozedUntil", { at: formatInboxTime(item.snoozed_until) })}
			</p>,
		);
	}
	if (notes.length === 0) return null;
	return (
		<Block title={item.task ? t("inbox.detail.task") : t(`inbox.state.${item.state}`)}>
			<div className="flex flex-col gap-2">{notes}</div>
		</Block>
	);
}

function Block({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
	return (
		<section className="flex flex-col gap-2">
			<div className="flex flex-col gap-0.5">
				<h3 className="text-foreground text-sm font-semibold">{title}</h3>
				{hint && <p className="text-muted-foreground text-xs">{hint}</p>}
			</div>
			{children}
		</section>
	);
}