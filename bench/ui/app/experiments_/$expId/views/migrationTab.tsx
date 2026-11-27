import { skipToken } from "@reduxjs/toolkit/query/react";
import { Link } from "@tanstack/react-router";
import "@/components/verdict/uf4aI18n";
import { MigrationReportView } from "@/components/migration/migrationReport";
import { ApplyPanel } from "@/components/modelswitch/applyPanel";
import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { GradeSourceTag } from "@/components/verdict/gradeSourceTag";
import { cn } from "@/lib/utils";
import type { MigrationParams, MigrationResponse } from "@/lib/types/bench";
import { useListPoliciesQuery } from "@/lib/store/apis/governApi";
import { useGetCostExplainQuery, useGetMigrationQuery } from "@/lib/store/apis/migrationApi";
import { useGetModelSwitchConfigQuery } from "@/lib/store/apis/modelSwitchApi";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";
import "./fw11I18n";
import "./hw10I18n";

/**
 * The Migration tab (docs/model-migration.md): moving from one arm to the others. ?from= is one arm
 * (default A), ?to= a comma list (default every other arm); both are picked from the experiment's arms.
 */
/** The migration request of the tab's URL state; empty values fall back to the server defaults. */
export function migrationParams(
	expId: string,
	search: {
		from?: string | null;
		to?: string | null;
		condition?: string | null;
		alpha?: string | null;
		grade_source?: string | null;
		policy?: string | null;
	},
): MigrationParams & { policy?: string } {
	// N280: policy is a rule set file name from policies/ or "none"; left out, the comparison's own choice.
	const out: MigrationParams & { policy?: string } = { expId };
	for (const key of ["from", "to", "condition", "alpha", "grade_source", "policy"] as const) {
		const value = search[key];
		if (value) out[key] = value;
	}
	return out;
}

export function MigrationTab({ expId, arms, evaluationId }: { expId: string; arms: readonly string[]; evaluationId?: string }) {
	const { t } = useTranslation("bench");
	const { t: tf } = useTranslation("uf4a");
	const [from, setFrom] = useQueryState("from", parseAsString);
	const [to, setTo] = useQueryState("to", parseAsString);
	const [condition] = useQueryState("condition", parseAsString);
	// alpha and grade_source come along from a bookmark (legacy /migration redirect included); the
	// server honours both, so the report must be the one the address bar names.
	const [alpha] = useQueryState("alpha", parseAsString);
	const [gradeSource] = useQueryState("grade_source", parseAsString);
	const [policy, setPolicy] = useQueryState("policy", parseAsString);
	const report = useGetMigrationQuery(migrationParams(expId, { from, to, condition, alpha, grade_source: gradeSource, policy }));
	// M4-2: the average cost per run of the pair, the same query as the Cost tab (shared cache).
	const costExplain = useGetCostExplainQuery(arms.length === 2 ? { expId } : skipToken);
	const meanCost = costExplain.data
		? { [costExplain.data.a.arm]: costExplain.data.a.cost_per_run, [costExplain.data.b.arm]: costExplain.data.b.cost_per_run }
		: undefined;
	// Only the response to the current selection is shown: RTK keeps the previous one in data while a
	// changed bookmark loads, and its settings would sit beside the new controls (QC2-27-07).
	const current = report.currentData;
	const pending = !current && !report.error;
	const fromArm = from ?? current?.from.arm ?? arms[0];
	const targets = arms.filter((arm) => arm !== fromArm);
	const chosen = to ? to.split(",").filter((arm) => targets.includes(arm)) : targets;
	const toggle = (arm: string) => {
		const next = chosen.includes(arm) ? chosen.filter((a) => a !== arm) : [...chosen, arm];
		// Every other arm is the server default, so the full set leaves the URL clean.
		void setTo(next.length === 0 || next.length === targets.length ? null : targets.filter((a) => next.includes(a)).join(","));
	};

	return (
		<div className="flex flex-col gap-4" data-testid="migration-screen">
			{/* M4-29: the controls fold away under one plain title. */}
			<details className="group rounded-lg border px-4 py-3" data-testid="migration-controls">
				<summary className="cursor-pointer text-sm font-medium select-none">{tf("migration.changeCompared")}</summary>
				<div className="mt-3 flex flex-wrap items-end gap-x-6 gap-y-3 text-sm">
					<div className="flex flex-col gap-1">
						<span className="text-muted-foreground text-xs" id="migration-from-label">
							{t("migration.fromLabel")}
						</span>
						<div className="flex flex-wrap gap-1" role="radiogroup" aria-labelledby="migration-from-label">
							{arms.map((arm) => (
								<ArmChip
									key={arm}
									arm={arm}
									active={arm === fromArm}
									role="radio"
									onClick={() => {
										void setFrom(arm === arms[0] ? null : arm);
										void setTo(null);
									}}
									testId={`migration-from-${arm}`}
								/>
							))}
						</div>
					</div>
					<div className="flex flex-col gap-1">
						<span className="text-muted-foreground text-xs" id="migration-to-label">
							{t("migration.toLabel")}
						</span>
						<div className="flex flex-wrap gap-1" role="group" aria-labelledby="migration-to-label">
							{targets.map((arm) => (
								<ArmChip key={arm} arm={arm} active={chosen.includes(arm)} onClick={() => toggle(arm)} testId={`migration-to-${arm}`} />
							))}
						</div>
					</div>
					<RuleSetSelect value={policy ?? ""} onChange={(v) => void setPolicy(v || null)} />
					{current && <EffectiveSettings report={current} />}
				</div>
			</details>
			{current && <RuleSetLine applied={current.policy} chosen={policy} />}
			{pending ? (
				<BlockSkeleton rows={6} testId="migration-report-loading" />
			) : report.error || !current ? (
				<LoadError title={t("migration.loadError")} error={report.error} onRetry={() => void report.refetch()} testId="migration-error" />
			) : (
				<MigrationReportView report={current} meanCost={meanCost} />
			)}
			{evaluationId ? arms.length >= 2 && <MigrationApply evaluationId={evaluationId} /> : <RoutingNote expId={expId} />}
		</div>
	);
}

/** Rule set names from the policies folder: file names without .yaml, as Releases > Rules lists them. */
export function ruleSetNames(files: readonly { file: string; valid: boolean }[]): string[] {
	return files.filter((p) => p.valid).map((p) => p.file.replace(/\.ya?ml$/, ""));
}

/**
 * N280 (D19): the rule set the advice is checked against. The first option is the comparison's own
 * choice (the one it froze, else none); "none" gives the statistics alone.
 */
function RuleSetSelect({ value, onChange }: { value: string; onChange: (value: string) => void }) {
	const { t } = useTranslation("hw10");
	const list = useListPoliciesQuery();
	const names = ruleSetNames(list.data?.policies ?? []);
	if (value && value !== "none" && !names.includes(value)) names.push(value);
	return (
		<label className="flex flex-col gap-1">
			<span className="text-muted-foreground text-xs">{t("ruleSet.label")}</span>
			<select
				className="border-input bg-background h-8 rounded-md border px-2 text-xs"
				value={value}
				onChange={(e) => onChange(e.target.value)}
				data-testid="migration-policy"
			>
				<option value="">{t("ruleSet.own")}</option>
				<option value="none">{t("ruleSet.none")}</option>
				{names.map((name) => (
					<option key={name} value={name}>
						{name}
					</option>
				))}
			</select>
		</label>
	);
}

/** The rule set the report followed, named by its file, read from the response. */
function RuleSetLine({ applied, chosen }: { applied: string; chosen: string | null }) {
	const { t } = useTranslation("hw10");
	const text = applied ? t("ruleSet.applied", { file: applied }) : chosen === "none" ? t("ruleSet.noneChosen") : t("ruleSet.noneApplied");
	return (
		<p className="text-muted-foreground text-sm" data-testid="migration-rule-set">
			{text}
		</p>
	);
}

/** D23: where a routing file comes from, for a comparison no evaluation made. */
function RoutingNote({ expId }: { expId: string }) {
	const { t } = useTranslation("hw10");
	const { t: tf } = useTranslation("uf4a");
	return (
		<p className="text-muted-foreground rounded-lg border px-4 py-3 text-sm" role="note" data-testid="migration-routing-note">
			{t("routingNote")} {/* M4-32: the line leads somewhere: the Actions tab, where a model switch is started. */}
			<Link
				to="/experiments/$expId"
				params={{ expId }}
				search={{ tab: "actions" } as never}
				className="text-primary font-medium hover:underline"
				data-testid="migration-open-actions"
			>
				{tf("migration.openActions")}
			</Link>
		</p>
	);
}

/** The server's error.message of an RTK Query error, else "". */
function errorText(error: unknown): string {
	const e = error as { data?: { error?: { message?: string } } } | undefined;
	return e?.data?.error?.message ?? "";
}

/**
 * "Apply to my Bifrost" for a comparison an evaluation made: the same panel as the evaluation's
 * model switch report. The server builds the routing rules only for two or more models of one
 * agent and answers 400 "not a model comparison" otherwise, which is shown instead of the panel.
 */
export function MigrationApply({ evaluationId }: { evaluationId: string }) {
	const { t } = useTranslation("fw11");
	const q = useGetModelSwitchConfigQuery({ id: evaluationId, options: {} });
	if (q.isLoading) {
		return (
			<p className="text-muted-foreground text-sm" data-testid="migration-apply-loading">
				{t("apply.loading")}
			</p>
		);
	}
	if (q.error || !q.data) {
		const reason = errorText(q.error);
		if (!reason) return null;
		return (
			<p className="text-muted-foreground rounded-lg border px-4 py-3 text-sm" role="note" data-testid="migration-apply-unavailable">
				{t("apply.unavailable", { reason })}
			</p>
		);
	}
	if (!q.data.ready) return null;
	return <ApplyPanel evaluationId={evaluationId} ruleCount={q.data.config.governance.routing_rules.length} />;
}

/** The settings the rendered report was built with, read from the response, never from the URL. */
export function EffectiveSettings({ report }: { report: MigrationResponse }) {
	// M4-29: no LIVE pill and no alpha label; only a practice report is marked, and the grades read.
	return (
		<span className="flex items-center gap-2" data-testid="migration-effective">
			{report.mode !== "live" && <ModeBadge mode={report.mode} />}
			<GradeSourceTag source={report.grade_source || undefined} testId="migration-source" />
		</span>
	);
}

function ArmChip({
	arm,
	active,
	onClick,
	role,
	testId,
}: {
	arm: string;
	active: boolean;
	onClick: () => void;
	role?: "radio";
	testId: string;
}) {
	return (
		<button
			type="button"
			role={role}
			aria-checked={role ? active : undefined}
			aria-pressed={role ? undefined : active}
			onClick={onClick}
			data-testid={testId}
			className={cn(
				"focus-visible:ring-ring/50 min-w-8 cursor-pointer rounded-sm border px-2.5 py-1 font-mono text-xs transition-colors focus-visible:ring-[3px] focus-visible:outline-none",
				active ? "bg-primary text-primary-foreground border-primary" : "hover:bg-accent",
			)}
		>
			{arm}
		</button>
	);
}