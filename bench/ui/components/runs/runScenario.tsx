import { ArtifactFiles } from "@/components/runs/runArtifacts";
import { durationSeconds, hasScenarioEvidence, isServiceRole, toolCallCounts } from "@/components/scenario/scenario.utils";
import { ToneBadge } from "@/components/suites/suiteBadges";
import { formatBytes } from "@/components/suites/suites.utils";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatCount, formatDateTime, shortHash } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { Answer, RunDetail, ServiceResult, SessionResult, StateFile, ToolCall } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { ChevronDown, ChevronRight, CircleCheck, CircleX, Network, Zap } from "lucide-react";
import { Fragment, useId, useState } from "react";
import { useTranslation } from "react-i18next";

/**
 * A run's scenario evidence: tool calls, sessions, answer, exported state, artifact files and
 * services. Each part renders only when the run recorded it, and a plain diff run renders nothing
 * at all, so a screen never shows an empty box for evidence that does not apply.
 */
export function RunScenarioEvidence({ detail, arm }: { detail: RunDetail; arm: string }) {
	if (!hasScenarioEvidence(detail)) return null;
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid={`run-scenario-${arm}`}>
			{detail.services && detail.services.length > 0 && <ServicesList services={detail.services} arm={arm} />}
			{detail.sessions && detail.sessions.length > 0 && <SessionsTimeline sessions={detail.sessions} arm={arm} />}
			{detail.tool_calls && detail.tool_calls.length > 0 && <ToolCallTable calls={detail.tool_calls} arm={arm} />}
			{detail.answer && <AnswerBlock answer={detail.answer} />}
			{detail.state && detail.state.length > 0 && <StateFiles files={detail.state} arm={arm} />}
			{detail.artifacts && detail.artifacts.length > 0 && (
				<ArtifactFiles files={detail.artifacts} runId={detail.run.run_id} arm={arm} sealed={detail.sealed} sealError={detail.seal_error} />
			)}
		</div>
	);
}

function Part({ title, description, children, testId }: { title: string; description?: string; children: React.ReactNode; testId: string }) {
	return (
		<section className="flex min-w-0 flex-col gap-2" data-testid={testId}>
			<div className="flex flex-col gap-0.5">
				<h3 className="text-foreground text-xs font-semibold tracking-wide uppercase">{title}</h3>
				{description ? <p className="text-muted-foreground max-w-prose text-xs">{description}</p> : null}
			</div>
			{children}
		</section>
	);
}

export interface ToolCallTableProps {
	calls: readonly ToolCall[];
	arm: string;
	/** Calls (by seq) open at first render. */
	initialOpen?: readonly number[];
}

/**
 * The tool proxy ledger as a table, in call order. Each row's arguments and result open below it
 * from a button (keyboard reachable, aria-expanded), and are shown as plain text: they are agent-
 * and tool-written and are never parsed or rendered as HTML.
 */
export function ToolCallTable({ calls, arm, initialOpen = [] }: ToolCallTableProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const idPrefix = useId();
	const [open, setOpen] = useState<ReadonlySet<number>>(() => new Set(initialOpen));
	const counts = toolCallCounts(calls);
	const toggle = (seq: number) =>
		setOpen((prev) => {
			const next = new Set(prev);
			if (next.has(seq)) next.delete(seq);
			else next.add(seq);
			return next;
		});

	return (
		<Part title={t("runs.scenario.tools.title")} testId={`run-tools-${arm}`}>
			<p className="text-muted-foreground flex flex-wrap gap-x-3 text-xs" data-testid={`run-tools-${arm}-counts`}>
				<span>{t("runs.scenario.tools.summary", { count: counts.calls })}</span>
				<span className={counts.errors > 0 ? "text-chart-error-ink" : undefined}>
					{t("runs.scenario.tools.errors", { count: counts.errors })}
				</span>
				<span className={counts.faults > 0 ? "text-chart-warning-ink" : undefined}>
					{t("runs.scenario.tools.faults", { count: counts.faults })}
				</span>
				{counts.other > 0 && (
					<span title={t("runs.scenario.tools.otherHint")}>{t("runs.scenario.tools.other", { count: counts.other })}</span>
				)}
				{counts.unparsed > 0 && (
					<span className="text-chart-warning-ink" title={t("runs.scenario.tools.unparsedHint")}>
						{t("runs.scenario.tools.unparsed", { count: counts.unparsed })}
					</span>
				)}
			</p>
			<div className="min-w-0 rounded-sm border">
				<Table aria-label={t("runs.scenario.tools.label", { arm })} data-testid={`run-tools-${arm}-table`}>
					<TableHeader>
						<TableRow>
							<TableHead scope="col" className="w-10 text-right">
								{t("runs.scenario.tools.seq")}
							</TableHead>
							<TableHead scope="col">{t("runs.scenario.tools.server")}</TableHead>
							<TableHead scope="col">{t("runs.scenario.tools.tool")}</TableHead>
							<TableHead scope="col">{t("runs.scenario.tools.result")}</TableHead>
							<TableHead scope="col" className="text-right">
								{t("runs.scenario.tools.latency")}
							</TableHead>
							<TableHead scope="col" className="text-right">
								{t("runs.scenario.tools.details")}
							</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{calls.map((call) => (
							<ToolCallRows
								key={call.seq}
								call={call}
								open={open.has(call.seq)}
								onToggle={() => toggle(call.seq)}
								detailsId={`${idPrefix}-call-${call.seq}`}
								locale={locale}
							/>
						))}
					</TableBody>
				</Table>
			</div>
		</Part>
	);
}

function ToolCallRows({
	call,
	open,
	onToggle,
	detailsId,
	locale,
}: {
	call: ToolCall;
	open: boolean;
	onToggle: () => void;
	detailsId: string;
	locale: string;
}) {
	const { t } = useTranslation("bench");
	return (
		<Fragment>
			<TableRow data-testid={`run-tool-call-${call.seq}`} data-state={open ? "selected" : undefined} data-error={call.is_error}>
				<TableCell className="text-muted-foreground text-right align-top font-mono text-xs tabular-nums">{call.seq}</TableCell>
				<TableCell className="align-top font-mono text-xs">{call.server}</TableCell>
				<TableCell className="max-w-xs align-top font-mono text-xs break-all whitespace-normal">
					{call.tool}
					{call.kind && (
						<span className="text-muted-foreground ml-2 font-sans" data-testid={`run-tool-call-${call.seq}-kind`}>
							{t(`runs.scenario.tools.kind.${call.kind}`, { defaultValue: call.kind })}
						</span>
					)}
					{call.unparsed && (
						<ToneBadge tone="warn" hint={call.unparsed_reason} testId={`run-tool-call-${call.seq}-unparsed`}>
							{t("runs.scenario.tools.unparsedBadge")}
						</ToneBadge>
					)}
					{call.session && (
						<span className="text-muted-foreground ml-2 font-sans">{t("runs.scenario.tools.session", { session: call.session })}</span>
					)}
				</TableCell>
				<TableCell className="align-top whitespace-normal">
					<span className="flex flex-wrap items-center gap-1">
						<ToneBadge tone={call.is_error ? "bad" : "good"} icon={call.is_error ? CircleX : CircleCheck}>
							{call.is_error ? t("runs.scenario.tools.error") : t("runs.scenario.tools.ok")}
						</ToneBadge>
						{call.status ? (
							<span className="text-muted-foreground font-mono text-[11px]">
								{t("runs.scenario.tools.status", { status: call.status })}
							</span>
						) : null}
						{call.fault && (
							<ToneBadge tone="warn" icon={Zap} hint={t("runs.scenario.tools.faultHint")} testId={`run-tool-call-${call.seq}-fault`}>
								{t("runs.scenario.tools.fault", { fault: call.fault })}
							</ToneBadge>
						)}
						{call.undelivered && (
							<ToneBadge tone="neutral" hint={t("runs.scenario.tools.undeliveredHint")}>
								{t("runs.scenario.tools.undelivered")}
							</ToneBadge>
						)}
					</span>
				</TableCell>
				<TableCell className="text-right align-top text-xs tabular-nums">
					{t("runs.scenario.tools.ms", { value: formatCount(call.latency_ms, locale) })}
				</TableCell>
				<TableCell className="text-right align-top">
					<button
						type="button"
						aria-expanded={open}
						aria-controls={detailsId}
						aria-label={t(open ? "runs.scenario.tools.hideAria" : "runs.scenario.tools.showAria", { seq: call.seq })}
						onClick={onToggle}
						data-testid={`run-tool-call-${call.seq}-toggle`}
						className={cn(
							"focus-visible:ring-ring/50 inline-flex cursor-pointer items-center gap-1 rounded-sm border px-2 py-1 text-xs font-medium transition-colors focus-visible:ring-[3px] focus-visible:outline-none",
							open ? "border-primary bg-primary/10" : "hover:bg-accent",
						)}
					>
						{open ? <ChevronDown className="size-3.5" aria-hidden="true" /> : <ChevronRight className="size-3.5" aria-hidden="true" />}
						{open ? t("runs.scenario.tools.hide") : t("runs.scenario.tools.show")}
					</button>
				</TableCell>
			</TableRow>
			{open && (
				<TableRow id={detailsId} data-testid={`run-tool-call-${call.seq}-details`} className="hover:bg-transparent">
					<TableCell colSpan={6} className="whitespace-normal">
						<div className="grid min-w-0 grid-cols-1 gap-3 py-1 xl:grid-cols-2">
							<InertText label={t("runs.scenario.tools.arguments")} text={call.arguments} hash={call.args_sha256} />
							<InertText label={t("runs.scenario.tools.resultText")} text={call.result} hash={call.result_sha256} />
						</div>
					</TableCell>
				</TableRow>
			)}
		</Fragment>
	);
}

/** Recorded text as text: React escapes it, nothing is parsed as markup or made a link. */
function InertText({ label, text, hash }: { label: string; text: string | undefined; hash: string }) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<span className="text-muted-foreground flex flex-wrap items-baseline justify-between gap-2 text-xs">
				<span className="text-foreground font-medium">{label}</span>
				{hash && (
					<span className="font-mono text-[11px]" title={hash}>
						{t("runs.scenario.tools.hash", { hash: shortHash(hash) })}
					</span>
				)}
			</span>
			<pre className="bg-muted/50 text-foreground max-h-72 min-w-0 overflow-auto rounded-sm border px-2 py-1.5 font-mono text-[11px] leading-relaxed break-all whitespace-pre-wrap">
				{text ? text : t("runs.scenario.tools.none")}
			</pre>
		</div>
	);
}

/** Sessions in run order: when each ran, how long, and how it ended. */
export function SessionsTimeline({ sessions, arm }: { sessions: readonly SessionResult[]; arm: string }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const ordered = [...sessions].sort((a, b) => a.index - b.index);
	return (
		<Part title={t("runs.scenario.sessions.title")} description={t("runs.scenario.sessions.description")} testId={`run-sessions-${arm}`}>
			<ol className="flex min-w-0 flex-col gap-1.5 border-l pl-3" aria-label={t("runs.scenario.sessions.label", { arm })}>
				{ordered.map((session) => {
					const seconds = durationSeconds(session.started, session.ended);
					const failed = session.timed_out || session.exit_code !== 0;
					return (
						<li
							key={session.index}
							className="relative flex min-w-0 flex-col gap-0.5 text-xs"
							data-testid={`run-session-${session.index}`}
							data-state={session.timed_out ? "timed_out" : failed ? "failed" : "ok"}
						>
							<span
								className={cn(
									"absolute top-1.5 -left-[17px] size-2 rounded-full border",
									failed ? "border-chart-error bg-chart-error/40" : "border-chart-success bg-chart-success/40",
								)}
								aria-hidden="true"
							/>
							<span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
								<span className="text-foreground font-medium">{t("runs.scenario.sessions.index", { index: session.index })}</span>
								<span className="text-foreground font-mono">{session.id}</span>
								{session.timed_out ? (
									<ToneBadge tone="bad">{t("runs.scenario.sessions.timedOut")}</ToneBadge>
								) : (
									<ToneBadge tone={session.exit_code === 0 ? "good" : "bad"}>
										{t("runs.scenario.sessions.exit", { code: session.exit_code })}
									</ToneBadge>
								)}
								{seconds !== null && (
									<span className="text-muted-foreground tabular-nums">
										{t("runs.scenario.sessions.duration", { value: formatCount(seconds, locale) })}
									</span>
								)}
							</span>
							<span className="text-muted-foreground flex min-w-0 flex-wrap gap-x-3 font-mono text-[11px]">
								<span>{formatDateTime(session.started, locale) ?? session.started}</span>
								{session.stdout_path && (
									<span className="break-all">{t("runs.scenario.sessions.stream", { path: session.stdout_path })}</span>
								)}
							</span>
						</li>
					);
				})}
			</ol>
		</Part>
	);
}

/** The exported answer as plain text, with where it came from. A missing answer says so. */
export function AnswerBlock({ answer }: { answer: Answer }) {
	const { t } = useTranslation("bench");
	const source = answer.source === "file" || answer.source === "final_message" || answer.source === "missing" ? answer.source : null;
	return (
		<Part title={t("runs.scenario.answer.title")} testId="run-answer">
			<p className="text-xs" data-testid="run-answer-source" data-source={answer.source}>
				<span className="text-muted-foreground">{t("runs.scenario.answer.source")}: </span>
				<span className={cn("font-medium", answer.source === "missing" ? "text-chart-error-ink" : "text-foreground")}>
					{source ? t(`runs.scenario.answer.${source}`) : answer.source}
				</span>
			</p>
			{answer.source !== "missing" && (
				<pre className="bg-muted/50 text-foreground max-h-72 min-w-0 overflow-auto rounded-sm border px-3 py-2 font-sans text-xs leading-relaxed break-words whitespace-pre-wrap">
					{answer.text ? answer.text : t("runs.scenario.answer.empty")}
				</pre>
			)}
			<p className="text-muted-foreground flex flex-wrap gap-x-3 text-xs">
				{answer.sha256 && (
					<span className="font-mono text-[11px]" title={answer.sha256}>
						{t("runs.scenario.answer.hash", { hash: shortHash(answer.sha256) })}
					</span>
				)}
				{answer.truncated && <span>{t("runs.scenario.answer.truncated")}</span>}
			</p>
		</Part>
	);
}

/** Exported state files: service, export, size and hash. A failed export shows its error. */
export function StateFiles({ files, arm }: { files: readonly StateFile[]; arm: string }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	return (
		<Part title={t("runs.scenario.state.title")} testId={`run-state-${arm}`}>
			<div className="min-w-0 rounded-sm border">
				<Table aria-label={t("runs.scenario.state.label", { arm })}>
					<TableHeader>
						<TableRow>
							<TableHead scope="col">{t("runs.scenario.state.service")}</TableHead>
							<TableHead scope="col">{t("runs.scenario.state.file")}</TableHead>
							<TableHead scope="col" className="text-right">
								{t("runs.scenario.state.size")}
							</TableHead>
							<TableHead scope="col">{t("runs.scenario.state.hash")}</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{files.map((file) => (
							<TableRow key={file.path || `${file.service}/${file.id}`} data-testid={`run-state-${file.service}-${file.id}`}>
								<TableCell className="font-mono text-xs">{file.service}</TableCell>
								<TableCell className="font-mono text-xs break-all whitespace-normal" title={file.path}>
									{file.id}
								</TableCell>
								<TableCell className="text-right text-xs tabular-nums">{file.error ? "" : formatBytes(file.bytes, locale)}</TableCell>
								<TableCell className="font-mono text-xs whitespace-normal">
									{file.error ? (
										<span className="text-chart-error-ink break-words">{t("runs.scenario.state.failed", { error: file.error })}</span>
									) : (
										<span title={file.sha256}>{shortHash(file.sha256)}</span>
									)}
								</TableCell>
							</TableRow>
						))}
					</TableBody>
				</Table>
			</div>
		</Part>
	);
}

/** Services that ran beside the agent: ready or the error that kept one from coming up. */
export function ServicesList({ services, arm }: { services: readonly ServiceResult[]; arm: string }) {
	const { t } = useTranslation("bench");
	return (
		<Part title={t("runs.scenario.services.title")} description={t("runs.scenario.services.description")} testId={`run-services-${arm}`}>
			<ul className="flex min-w-0 flex-col gap-1.5" aria-label={t("runs.scenario.services.label", { arm })}>
				{services.map((service) => (
					<li
						key={service.id}
						className="flex min-w-0 flex-col gap-0.5 rounded-sm border px-2 py-1.5 text-xs"
						data-testid={`run-service-${service.id}`}
						data-ready={service.ready}
					>
						<span className="flex min-w-0 flex-wrap items-center gap-1.5">
							<span className="text-foreground font-mono font-medium">{service.id}</span>
							{service.role && (
								<span className="text-muted-foreground">
									{isServiceRole(service.role) ? t(`scenario.role.${service.role}`) : service.role}
								</span>
							)}
							<ToneBadge tone={service.ready ? "good" : "bad"} icon={service.ready ? CircleCheck : CircleX}>
								{service.ready ? t("runs.scenario.services.ready") : t("runs.scenario.services.notReady")}
							</ToneBadge>
							{service.proxied && (
								<ToneBadge tone="neutral" icon={Network}>
									{t("runs.scenario.services.proxied")}
								</ToneBadge>
							)}
						</span>
						{service.image && (
							<span className="text-muted-foreground font-mono text-[11px] break-all" title={service.image_id}>
								{t("runs.scenario.services.image", { image: service.image })}
							</span>
						)}
						{service.error && <span className="text-chart-error-ink break-words">{service.error}</span>}
					</li>
				))}
			</ul>
		</Part>
	);
}