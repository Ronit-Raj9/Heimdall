import { ReportCard } from "@/components/reportcard/reportCard";
import { capabilityName, pct, usd } from "@/components/reportcard/reportCard.utils";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { AgentLeaderboardResponse } from "@/lib/store/apis/agentLeaderboardApi";
import type { Evaluation, Report } from "@/lib/store/apis/evaluationsApi";
import { errorStatus } from "@/lib/store/apis/session";
import { sharedExportUrl, useGetSharedQuery, type SharedItem } from "@/lib/store/apis/shareApi";
import type { Leaderboard, RunDetail, VerdictPayload } from "@/lib/types/bench";
import "@/lib/types/shareI18n";
import { Download, EyeOff, FileCode2 } from "lucide-react";
import { usePageTitle } from "@/lib/routing/pageTitle";
import { type ReactNode, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { formatUsd } from "@/lib/money";
import "./hw16I18n";
import "@/components/share/uf8bI18n";
import "@/components/share/ufd4I18n";
import { headline, costNumbers } from "@/components/verdict/verdictSummary";
import { armOrder } from "@/components/verdict/verdict.utils";
import { observedNumber } from "@/components/verdict/armCost";
import { DEFAULT_MIN_RUNS } from "@/components/leaderboard/agentBoard.utils";
import { getLocale } from "@/lib/i18n";
import type { Ratio, Value } from "@/lib/types/bench";

/** GET /agents/{id}/report-card as the server sends it: the report, or null before any evaluation. */
interface AgentReportCard {
	agent: string;
	report: Report | null;
}

interface TraceStep {
	seq: number;
	kind: string;
	summary?: string;
	tool_name?: string;
	duration_ms?: number;
	is_error?: boolean;
}

interface RunTrace {
	steps?: { steps?: TraceStep[]; truncated?: boolean };
}

function formatDate(iso?: string): string {
	if (!iso) return "";
	const d = new Date(iso);
	return Number.isNaN(d.getTime())
		? iso
		: d.toLocaleDateString(undefined, {
				year: "numeric",
				month: "short",
				day: "numeric",
			});
}

/**
 * Moves the address bar to /s/<canonical> without a reload when the link was opened with an old
 * token that names the organization (fix wave 3, N293), so a link copied from the browser no
 * longer carries the org id. Exported for tests.
 */
export function replaceShareAddress(
	canonical: string,
	win: Pick<Window, "history" | "location"> | undefined = typeof window === "undefined" ? undefined : window,
) {
	if (!win || !canonical) return;
	const path = `/s/${encodeURIComponent(canonical)}`;
	if (win.location.pathname === path) return;
	win.history.replaceState(win.history.state, "", `${path}${win.location.search}${win.location.hash}`);
}

/** /s/<token>: the shared item, read-only. */
export default function SharedView({ token }: { token: string }) {
	const { t } = useTranslation("share");
	const shared = useGetSharedQuery(token);
	const canonical = shared.data?.canonical_token;
	useEffect(() => {
		if (canonical && canonical !== token) replaceShareAddress(canonical);
	}, [canonical, token]);

	usePageTitle(`${shared.data?.share.label || t("page.shared")} · Heimdall`);

	if (shared.isLoading) {
		return (
			<p className="text-muted-foreground text-sm" data-testid="shared-loading">
				{t("page.loading")}
			</p>
		);
	}
	if (shared.error || !shared.data) {
		const status = errorStatus(shared.error);
		const msg = status === 410 ? t("page.gone") : status === 404 ? t("page.notFound") : t("page.error");
		return (
			<div role="alert" className="flex flex-col items-start gap-3 rounded-xl border p-6" data-testid="shared-error">
				<p className="text-sm">{msg}</p>
				{status !== 404 && status !== 410 && (
					<Button type="button" variant="outline" size="sm" onClick={() => void shared.refetch()}>
						{t("page.retry")}
					</Button>
				)}
			</div>
		);
	}
	return <SharedItemView item={shared.data} token={token} />;
}

/** The item, by kind. Exported for tests. */
export function SharedItemView({ item, token }: { item: SharedItem; token: string }) {
	const { t } = useTranslation("share");
	const { share, data } = item;
	return (
		<article className="flex flex-col gap-6" data-testid={`shared-${share.kind}`}>
			<div className="flex flex-col gap-1">
				<p className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
					{share.org ? t("page.sharedBy", { org: share.org }) : t("page.shared")}
				</p>
				{share.label && <h1 className="text-2xl font-semibold tracking-tight">{share.label}</h1>}
				<div className="text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs">
					{share.expires_at && <span>{t("page.expires", { when: formatDate(share.expires_at) })}</span>}
					{share.created_by_name?.trim() && (
						<span data-testid="shared-made-by">{t("shared.madeBy", { ns: "ufd4", name: share.created_by_name.trim() })}</span>
					)}
					{share.hide_prompts && (
						<span className="flex items-center gap-1" data-testid="shared-prompts-hidden">
							<EyeOff className="size-3.5" aria-hidden="true" />
							{t("page.promptsHidden")}
						</span>
					)}
				</div>
			</div>
			{share.kind === "evaluation" && (
				<EvaluationPart
					evaluation={data.evaluation as Evaluation | null}
					report={data.report as Report | null}
					token={token}
					exports={share.exports ?? []}
				/>
			)}
			{share.kind === "report_card" && <ReportPart report={(data.report_card as AgentReportCard | null)?.report ?? null} />}
			{share.kind === "verdict" && (
				<VerdictPart verdict={data.verdict as SharedVerdict | null} experiment={data.experiment as SharedExperiment | null} />
			)}
			{share.kind === "run_trace" && <RunPart run={data.run as RunDetail | null} trace={data.trace as RunTrace | null} />}
			{share.kind === "leaderboard" &&
				(share.target === "agents" ? (
					<AgentBoardPart board={data.leaderboard as AgentLeaderboardResponse | null} />
				) : (
					<ModelBoardPart board={data.leaderboard as Leaderboard | null} />
				))}
		</article>
	);
}

function ReportPart({ report, actions }: { report: Report | null; actions?: ReactNode }) {
	const { t } = useTranslation("share");
	if (!report) return <p className="text-muted-foreground text-sm">{t("page.noReport")}</p>;
	return <ReportCard report={report} readOnly actions={actions} />;
}

function EvaluationPart({
	evaluation,
	report,
	token,
	exports,
}: {
	evaluation: Evaluation | null;
	report: Report | null;
	token: string;
	exports: string[];
}) {
	const { t } = useTranslation("share");
	const actions =
		exports.length > 0 ? (
			<div className="flex flex-wrap gap-2 print:hidden" data-testid="shared-exports">
				{exports.includes("html") && (
					<Button asChild variant="outline" size="sm">
						<a href={sharedExportUrl(token, "html")} download>
							<Download aria-hidden="true" />
							HTML
						</a>
					</Button>
				)}
				{exports.includes("md") && (
					<Button asChild variant="outline" size="sm">
						<a href={sharedExportUrl(token, "md")} download>
							<FileCode2 aria-hidden="true" />
							{t("markdown")}
						</a>
					</Button>
				)}
			</div>
		) : undefined;
	return (
		<>
			{evaluation?.spec?.name && <h2 className="text-lg font-semibold">{evaluation.spec.name}</h2>}
			<ReportPart report={report} actions={actions} />
		</>
	);
}

function Facts({ items }: { items: Array<[string, ReactNode]> }) {
	return (
		<dl className="grid grid-cols-2 gap-x-6 gap-y-3 rounded-xl border p-4 text-sm sm:grid-cols-4">
			{items.map(([k, v]) => (
				<div key={k} className="min-w-0">
					<dt className="text-muted-foreground text-xs">{k}</dt>
					<dd className="truncate font-medium">{v}</dd>
				</div>
			))}
		</dl>
	);
}

const ACTIONS = ["ship", "hold", "no_decision", "fix_harness"] as const;
const DECISIONS = ["improved", "regressed", "inconclusive", "invalid", "score"] as const;

/** One setup's naive score: passes over runs, as a percent, or null with no runs. Exported for tests. */
export function naiveScore(r: { num: number; den: number } | undefined): number | null {
	return r && r.den > 0 ? r.num / r.den : null;
}

/** Points with a sign: 0.125 is "+13 pts". */
function points(d: number): string {
	return `${d > 0 ? "+" : ""}${Math.round(d * 100)} pts`;
}

/** The effect line: the change with its approximate range when the server sent one. Exported for tests. */
export function effectText(effect: VerdictPayload["verdict"]["effect"], withRange: (e: string, lo: string, hi: string) => string): string {
	if (!effect) return "-";
	const e = points(effect.d_bar);
	return effect.interval ? withRange(e, points(effect.interval[0]), points(effect.interval[1])) : e;
}

/** The verdict part as a share link sends it; `arm_metrics` (median cost per run) only if a server adds it. */
type SharedVerdict = VerdictPayload & { arm_metrics?: Record<string, Record<string, Value>> };

/** The experiment part of a shared verdict: only the fields this page reads, every one optional. */
interface SharedExperiment {
	spent_usd?: Value | number;
	started_at?: string;
	finished_at?: string;
	setups?: { arm: string; agent?: string; model?: string }[];
}

/**
 * One setup's rate line (M8-68): the rate with the runs it rests on, or "too few runs to rate"
 * under DEFAULT_MIN_RUNS runs, the rule the app uses. Exported for tests.
 */
export function rateOrTooFew(r: Ratio | undefined, min = DEFAULT_MIN_RUNS): { rate: number | null; tooFew: boolean } {
	const rate = naiveScore(r);
	if (rate === null) return { rate: null, tooFew: false };
	return { rate, tooFew: (r?.den ?? 0) < min };
}

/** Total spend of the shared experiment: the observed Value, or a bare number from an older server. Exported for tests. */
export function spentNumber(spent: SharedExperiment["spent_usd"]): number | null {
	if (typeof spent === "number") return Number.isFinite(spent) ? spent : null;
	return observedNumber(spent);
}

function VerdictPart({ verdict, experiment }: { verdict: SharedVerdict | null; experiment: SharedExperiment | null }) {
	const { t } = useTranslation("share");
	const { t: tu } = useTranslation("uf8b");
	const { t: td } = useTranslation("ufd4");
	const { t: tg } = useTranslation("glossary");
	const { t: tp } = useTranslation("experimentPage");
	if (!verdict) return <p className="text-muted-foreground text-sm">{t("page.noReport")}</p>;
	const v = verdict.verdict;
	const order = armOrder(verdict);
	// The same headline as the app's verdict page; the cheaper-setup line needs a per-setup cost, which only arm_metrics carries.
	const title = headline(
		v,
		order,
		{ partial: verdict.partial, naive: verdict.naive, costs: costNumbers(order, undefined, verdict.arm_metrics) },
		getLocale(),
		tp,
	);
	const spent = spentNumber(experiment?.spent_usd);
	const when = experiment?.finished_at || experiment?.started_at;
	const action = (ACTIONS as readonly string[]).includes(v.action) ? tu(`shared.actions.${v.action as (typeof ACTIONS)[number]}`) : v.action;
	const decision = (DECISIONS as readonly string[]).includes(v.decision) ? tg(`terms.${v.decision as (typeof DECISIONS)[number]}.label`) : v.decision;
	const effect = effectText(v.effect, (e, lo, hi) => tu("shared.interval", { effect: e, lo, hi }));
	const arms = Object.keys(verdict.naive ?? {}).sort();
	return (
		<section className="flex flex-col gap-4" data-testid="shared-verdict">
			<h2 className="text-2xl font-semibold tracking-tight" data-testid="shared-verdict-headline">
				{title}
			</h2>
			<p className="text-muted-foreground text-sm">{action}</p>
			{/* The result card: each setup's score with how many runs it rests on (M8-68). */}
			{arms.length > 0 && (
				<div className="grid gap-3 sm:grid-cols-2 print:grid-cols-2 print:break-inside-avoid" data-testid="shared-verdict-scores">
					{arms.map((arm) => {
						const r = verdict.naive[arm];
						const { rate, tooFew } = rateOrTooFew(r);
						const setup = experiment?.setups?.find((x) => x.arm === arm);
						const what = [setup?.agent, setup?.model].filter(Boolean).join(" · ");
						const cost = observedNumber(verdict.arm_metrics?.[arm]?.cost_usd);
						return (
							<div key={arm} className="rounded-xl border p-4" data-testid={`shared-setup-${arm}`}>
								<p className="text-muted-foreground text-xs">{tu("shared.setup", { arm })}</p>
								{what && <p className="truncate text-sm font-medium">{what}</p>}
								{rate === null ? (
									<p className="text-3xl font-semibold tabular-nums">-</p>
								) : tooFew ? (
									<>
										<p className="text-lg font-semibold">{td("shared.tooFew")}</p>
										<p className="text-muted-foreground text-xs tabular-nums">{td("shared.tooFewDetail", { den: r.den, min: DEFAULT_MIN_RUNS })}</p>
									</>
								) : (
									<p className="text-3xl font-semibold tabular-nums">
										{pct(rate)} <span className="text-muted-foreground text-xs font-normal">{td("shared.sample", { den: r.den })}</span>
									</p>
								)}
								<p className="text-muted-foreground text-xs tabular-nums">
									{td("shared.runs", { num: r.num, den: r.den })}
									{r.unknown > 0 && ` · ${tu("shared.unknownRuns", { count: r.unknown })}`}
								</p>
								{cost !== null && (
									<p className="text-muted-foreground text-xs tabular-nums">
										{td("shared.costPerRun")}: {formatUsd(cost)}
									</p>
								)}
							</div>
						);
					})}
				</div>
			)}
			<Facts
				items={[
					[t("page.decision"), decision],
					[t("page.effect"), effect],
					[tu("shared.tasks"), v.effect ? String(v.effect.n_tasks) : "-"],
					[t("page.suite"), verdict.suite],
					[t("page.guards"), String(v.guards?.length ?? 0)],
					...(spent !== null ? ([[td("shared.spent"), formatUsd(spent)]] as Array<[string, ReactNode]>) : []),
					...(when
						? ([[experiment?.finished_at ? td("shared.finished") : td("shared.started"), formatDate(when)]] as Array<[string, ReactNode]>)
						: []),
				]}
			/>
			{v.guards && v.guards.length > 0 && (
				<Table>
					<TableHeader>
						<TableRow>
							<TableHead>{t("page.guards")}</TableHead>
							<TableHead>{t("page.status")}</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{v.guards.map((g, i) => (
							<TableRow key={i}>
								<TableCell>{(g as { rule?: string }).rule ?? "-"}</TableCell>
								<TableCell>
									{String((g as { status?: string; result?: string }).status ?? (g as { result?: string }).result ?? "-")}
								</TableCell>
							</TableRow>
						))}
					</TableBody>
				</Table>
			)}
		</section>
	);
}

function RunPart({ run, trace }: { run: RunDetail | null; trace: RunTrace | null }) {
	const { t } = useTranslation("share");
	if (!run) return <p className="text-muted-foreground text-sm">{t("page.noReport")}</p>;
	const steps = trace?.steps?.steps ?? [];
	const cost = run.metrics?.cost_usd?.v;
	return (
		<section className="flex flex-col gap-4" data-testid="shared-run">
			<h2 className="text-lg font-semibold">
				{t("page.run")} {run.run.run_id}
			</h2>
			<Facts
				items={[
					[t("page.task"), run.slot.task],
					[t("page.agent"), run.run.agent?.config_id ?? run.slot.arm],
					[t("page.status"), String(run.run.outcome)],
					[t("page.cost"), typeof cost === "number" ? usd(cost) : "-"],
				]}
			/>
			<h3 className="text-sm font-medium">{t("page.timeline")}</h3>
			{steps.length === 0 ? (
				<p className="text-muted-foreground text-sm">{t("page.noSteps")}</p>
			) : (
				<>
					{trace?.steps?.truncated && <p className="text-muted-foreground text-xs">{t("page.stepsTruncated", { count: steps.length })}</p>}
					<ol className="divide-border/80 flex flex-col divide-y rounded-xl border text-sm" data-testid="shared-steps">
						{steps.map((s) => (
							<li key={s.seq} className="flex gap-3 px-4 py-2">
								<span className="text-muted-foreground w-8 shrink-0 text-xs tabular-nums">{s.seq}</span>
								<span className="w-28 shrink-0 text-xs font-medium">{s.tool_name || s.kind}</span>
								<span className={s.is_error ? "text-destructive min-w-0 flex-1 break-words" : "min-w-0 flex-1 break-words"}>
									{s.summary}
								</span>
							</li>
						))}
					</ol>
				</>
			)}
		</section>
	);
}

function AgentBoardPart({ board }: { board: AgentLeaderboardResponse | null }) {
	const { t } = useTranslation("share");
	const rows = board?.rows ?? [];
	if (rows.length === 0) return <p className="text-muted-foreground text-sm">{t("page.noRows")}</p>;
	const caps = board?.capabilities ?? [];
	return (
		<Table data-testid="shared-agent-board">
			<TableHeader>
				<TableRow>
					<TableHead>{t("page.rank")}</TableHead>
					<TableHead>{t("page.agent")}</TableHead>
					<TableHead>{t("page.model")}</TableHead>
					{caps.map((c) => (
						<TableHead key={c}>{capabilityName(c)}</TableHead>
					))}
					<TableHead>{t("leaderboard.costPerRun", { ns: "hw16" })}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((r, i) => (
					<TableRow key={`${r.agent}-${r.version}-${r.model}`}>
						<TableCell className="tabular-nums">{i + 1}</TableCell>
						<TableCell className="font-medium">
							{r.agent}
							{r.version > 0 ? ` v${r.version}` : ""}
						</TableCell>
						<TableCell>{r.model}</TableCell>
						{caps.map((c) => (
							<TableCell key={c} className="tabular-nums">
								{r.capabilities[c] ? pct(r.capabilities[c].score) : "-"}
							</TableCell>
						))}
						<TableCell className="tabular-nums">{formatUsd(r.cost_usd)}</TableCell>
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}

function ModelBoardPart({ board }: { board: Leaderboard | null }) {
	const { t } = useTranslation("share");
	const rows = board?.entries ?? [];
	if (rows.length === 0) return <p className="text-muted-foreground text-sm">{t("page.noRows")}</p>;
	return (
		<section className="flex flex-col gap-3">
			{board?.suite && <p className="text-muted-foreground text-sm">{board.suite}</p>}
			<Table data-testid="shared-model-board">
				<TableHeader>
					<TableRow>
						<TableHead>{t("page.rank")}</TableHead>
						<TableHead>{t("page.agent")}</TableHead>
						<TableHead>{t("page.model")}</TableHead>
						<TableHead>{t("page.score")}</TableHead>
						<TableHead>{t("page.runs")}</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{rows.map((r) => (
						<TableRow key={`${r.config_hash}-${r.model}`}>
							<TableCell className="tabular-nums">{r.rank}</TableCell>
							<TableCell className="font-medium">{r.agent}</TableCell>
							<TableCell>{r.model}</TableCell>
							<TableCell className="tabular-nums">{typeof r.success?.v === "number" ? pct(r.success.v) : "-"}</TableCell>
							<TableCell className="tabular-nums">{r.runs}</TableCell>
						</TableRow>
					))}
				</TableBody>
			</Table>
		</section>
	);
}