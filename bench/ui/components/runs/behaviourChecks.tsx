import "./w441I18n";
import "./ufw2I18n";

import { BehaviourEditor, saveErrorText } from "@/components/runs/behaviourEditor";
import { BlockSkeleton, LoadError, Section } from "@/components/runs/screenStates";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { canUse, useSession } from "@/lib/session/sessionContext";
import {
	useGetBehaviourChecksQuery,
	useGetExperimentBehaviourQuery,
	useGetRunBehaviourQuery,
	usePutBehaviourChecksMutation,
	type BehaviourRule,
	type ExperimentBehaviourResponse,
	type RuleResult,
	type RunBehaviourResponse,
} from "@/lib/store/apis/behaviourApi";
import { useState } from "react";
import { useTranslation } from "react-i18next";

function StatusWord({ result }: { result: RuleResult }) {
	const { t } = useTranslation("w441");
	if (result.status === "fired") return <Badge variant="destructive">{t("behaviour.seen")}</Badge>;
	if (result.status === "clear") return <Badge variant="secondary">{t("behaviour.notSeen")}</Badge>;
	return <Badge variant="outline">{t("behaviour.cannotTell")}</Badge>;
}

/** The run's results in show order: fired first, then cannot tell, then clear; and whether none could be read. */
export function behaviourOrder(results: readonly RuleResult[]): { fired: RuleResult[]; unknown: RuleResult[]; clear: RuleResult[]; allUnknown: boolean } {
	const fired = results.filter((r) => r.status === "fired");
	const clear = results.filter((r) => r.status === "clear");
	const unknown = results.filter((r) => r.status !== "fired" && r.status !== "clear");
	return { fired, unknown, clear, allUnknown: results.length > 0 && unknown.length === results.length };
}

/** The one reason every "cannot tell" result shares, so it is said once instead of on every row. */
export function sharedUnknownReason(unknown: readonly RuleResult[]): string | undefined {
	const first = unknown[0]?.reason?.trim();
	if (!first) return undefined;
	return unknown.every((r) => r.reason?.trim() === first) ? first : undefined;
}

/** True when no run of the experiment was checked yet: no arm, or every arm with zero runs. */
export function noRunsChecked(perArm: Record<string, { runs: number }>): boolean {
	return Object.values(perArm).every((c) => !c.runs);
}

function BehaviourRow({ r, title, runId, hideReason }: { r: RuleResult; title: string; runId: string; hideReason?: boolean }) {
	const { t } = useTranslation("w441");
	const { t: tw } = useTranslation("ufw2");
	return (
		<li className="flex flex-col gap-1" data-testid={`behaviour-run-${r.rule_id}`}>
			<div className="flex flex-wrap items-center gap-2">
				<span>{title}</span>
				<StatusWord result={r} />
				{r.status === "unknown" && r.reason && !hideReason && <span className="text-muted-foreground text-xs">{r.reason}</span>}
			</div>
			{/* Q-260: this check reads the agent's steps; an error only the tool proxy saw is not one of them. */}
			{r.rule_id === "ignores_tool_error" && (
				<p className="text-muted-foreground text-xs" data-testid="behaviour-proxy-errors-note">
					{tw("behaviour.proxyErrorsNote")}
				</p>
			)}
			{r.status === "fired" &&
				(r.hits ?? []).map((h, i) => (
					<div key={i} className="flex flex-wrap items-center gap-2 pl-4 font-mono text-xs">
						<a className="underline" href={`/runs/${encodeURIComponent(runId)}?tab=timeline&step=${h.seqs[0] ?? ""}`}>
							{h.summary}
						</a>
						{h.witnessed && <Badge variant="outline">{t("behaviour.witnessed")}</Badge>}
					</div>
				))}
		</li>
	);
}

/**
 * The rules on a run: fired first with the steps that matched, then those that could not tell and those
 * not seen, each folded. When no rule could read the steps, one line says so instead of a row per rule.
 */
export function RunBehaviourView({ data, rules, runId }: { data: RunBehaviourResponse; rules: BehaviourRule[]; runId: string }) {
	const { t } = useTranslation("w441");
	const titles = new Map(rules.map((r) => [r.id, r.title]));
	const order = behaviourOrder(data.results);
	if (order.allUnknown) {
		return (
			<p className="text-muted-foreground text-sm" data-testid="behaviour-run-unknown">
				{t("behaviour.allUnknown")}
			</p>
		);
	}
	const shared = sharedUnknownReason(order.unknown);
	const row = (r: RuleResult) => (
		<BehaviourRow key={r.rule_id} r={r} title={titles.get(r.rule_id) ?? r.rule_id} runId={runId} hideReason={Boolean(shared)} />
	);
	const fold = (list: RuleResult[], label: string, testId: string, note?: string) =>
		list.length > 0 && (
			<details data-testid={testId}>
				<summary className="text-muted-foreground cursor-pointer text-xs">{label}</summary>
				{note && (
					<p className="text-muted-foreground mt-2 text-xs" data-testid={`${testId}-reason`}>
						{t("behaviour.sharedReason", { reason: note })}
					</p>
				)}
				<ul className="mt-2 flex flex-col gap-3">{list.map(row)}</ul>
			</details>
		);
	return (
		<div className="flex flex-col gap-3 text-sm" data-testid="behaviour-run-list">
			{order.fired.length > 0 && <ul className="flex flex-col gap-3">{order.fired.map(row)}</ul>}
			{fold(order.unknown, t("behaviour.unknownCount", { count: order.unknown.length }), "behaviour-run-unknown-list", shared)}
			{fold(order.clear, t("behaviour.clearCount", { count: order.clear.length }), "behaviour-run-clear-list")}
		</div>
	);
}

/** The run page section "Behaviour checks" (lane I28). */
export function RunBehaviourChecks({ runId }: { runId: string }) {
	const { t } = useTranslation("w441");
	const res = useGetRunBehaviourQuery(runId);
	const checks = useGetBehaviourChecksQuery();
	const rules = [...(checks.data?.builtin ?? []), ...(checks.data?.rules ?? [])];
	return (
		<Section title={t("behaviour.title")} description={t("behaviour.description")} testId="run-behaviour">
			{res.isLoading ? (
				<BlockSkeleton rows={3} testId="run-behaviour-loading" />
			) : res.error || !res.data ? (
				<LoadError title={t("behaviour.error")} error={res.error} onRetry={() => void res.refetch()} testId="run-behaviour-error" />
			) : (
				<CardContent className="rounded-sm border px-4 py-4">
					<RunBehaviourView data={res.data} rules={rules} runId={runId} />
				</CardContent>
			)}
		</Section>
	);
}

/** Each rule over an experiment: per arm how many runs it fired on, split by the checked result. */
export function ExperimentBehaviourView({
	data,
	canEdit,
	onEdit,
}: {
	data: ExperimentBehaviourResponse;
	canEdit: boolean;
	onEdit: () => void;
}) {
	const { t } = useTranslation("w441");
	return (
		<div className="flex flex-col gap-3 text-sm">
			{canEdit && (
				<div>
					<Button size="sm" variant="outline" onClick={onEdit} data-testid="behaviour-edit">
						{t("behaviour.edit")}
					</Button>
				</div>
			)}
			{data.rules.length === 0 && <p className="text-muted-foreground">{t("behaviour.none")}</p>}
			<ul className="flex flex-col gap-3">
				{data.rules.map((r) => (
					<li key={r.rule.id} data-testid={`behaviour-exp-${r.rule.id}`}>
						<div className="font-medium">{r.rule.title}</div>
						{noRunsChecked(r.per_arm) ? (
							<p className="text-muted-foreground pl-4 text-xs" data-testid={`behaviour-exp-${r.rule.id}-none`}>
								{t("behaviour.noRunsYet")}
							</p>
						) : (
						<ul className="text-muted-foreground pl-4 text-xs">
							{Object.keys(r.per_arm)
								.sort()
								.map((arm) => {
									const c = r.per_arm[arm];
									return (
										<li key={arm}>
											{t("behaviour.perArm", { arm, fired: c.fired, runs: c.runs })} (
											{t("behaviour.split", { passed: c.fired_passed, failed: c.fired_failed })})
										</li>
									);
								})}
						</ul>
						)}
					</li>
				))}
			</ul>
			{data.partial && <p className="text-muted-foreground text-xs">{t("behaviour.partial")}</p>}
		</div>
	);
}

/** The Triage tab section "Behaviour checks" for one experiment, with the editor for admins. */
export function ExperimentBehaviourChecks({ expId }: { expId: string }) {
	const { t } = useTranslation("w441");
	const session = useSession();
	const res = useGetExperimentBehaviourQuery(expId);
	const checks = useGetBehaviourChecksQuery();
	const [save, saving] = usePutBehaviourChecksMutation();
	const [editing, setEditing] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const canEdit = canUse(session, "admin");
	return (
		<Section title={t("behaviour.title")} description={t("behaviour.description")} testId="triage-behaviour">
			{res.isLoading ? (
				<BlockSkeleton rows={3} testId="triage-behaviour-loading" />
			) : res.error || !res.data ? (
				<LoadError title={t("behaviour.error")} error={res.error} onRetry={() => void res.refetch()} testId="triage-behaviour-error" />
			) : (
				<Card className="px-4 py-4 shadow-none">
					{editing && checks.data ? (
						<BehaviourEditor
							initial={checks.data.rules}
							saving={saving.isLoading}
							error={error}
							onCancel={() => setEditing(false)}
							onSave={(rules) => {
								setError(null);
								save({ rules })
									.unwrap()
									.then(() => setEditing(false))
									.catch((e: unknown) => setError(saveErrorText(e, t("editor.saveError"))));
							}}
						/>
					) : (
						<ExperimentBehaviourView data={res.data} canEdit={canEdit} onEdit={() => setEditing(true)} />
					)}
				</Card>
			)}
		</Section>
	);
}