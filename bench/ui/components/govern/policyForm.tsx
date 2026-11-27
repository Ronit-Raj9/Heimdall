import { Button } from "@/components/ui/button";
import { NativeSelect } from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import { idProblem } from "@/lib/inputChecks";
import "@/lib/w4m0I18n";
import type { PolicyCheck, ReleasePolicy } from "@/lib/types/govern";
import { cn } from "@/lib/utils";
import {
	CheckCircle2,
	CircleAlert,
	DollarSign,
	Gauge,
	type LucideIcon,
	Plus,
	Repeat,
	ShieldAlert,
	SlidersHorizontal,
	Timer,
	Trash2,
} from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

/** Metrics a check can test (docs/release-policy.md); the field also accepts any per-run metric. */
export const POLICY_METRICS = [
	"verified_success",
	"false_completion",
	"integrity_violations",
	"retry_amplification",
	"cost_per_verified_success",
	"cost_usd",
	"latency_s",
];

/**
 * Test conditions a rule can read, as the dropdown offers them: normal runs, every condition, or one
 * PS2 fault. The values are the condition ids an evaluation's experiments use (bench/faults); a
 * benchmark with its own probe for a fault names that probe instead, kept as "custom".
 */
export const POLICY_CONDITIONS = [
	{ value: "", key: "conditionMain" },
	{ value: "clean", key: "conditionClean" },
	{ value: "all", key: "conditionAll" },
	{ value: "heimdall-model-down", key: "conditionModelDown" },
	{ value: "heimdall-broken-tool", key: "conditionBrokenTool" },
	{ value: "stale-notes", key: "conditionMisleading" },
] as const;

/** Whether a condition id is one the dropdown names (else it is shown as a custom one). */
export function isListedCondition(c: string | undefined): boolean {
	return POLICY_CONDITIONS.some((o) => o.value === (c ?? ""));
}

export type CheckKind = "no_regression" | "improved" | "max" | "min" | "equals";

export function checkKind(c: PolicyCheck): CheckKind {
	if (c.rule === "no_regression" || c.rule === "improved") return c.rule;
	if (c.min !== undefined) return "min";
	if (c.equals !== undefined) return "equals";
	return "max";
}

function thresholdOf(c: PolicyCheck): number | undefined {
	return c.max ?? c.min ?? c.equals;
}

/** Sets a check's rule kind, keeping the threshold when moving between max, min and equals. */
export function withKind(c: PolicyCheck, kind: CheckKind): PolicyCheck {
	const { rule: _r, max: _x, min: _n, equals: _e, ...rest } = c;
	if (kind === "no_regression" || kind === "improved") return { ...rest, rule: kind };
	return { ...rest, [kind]: thresholdOf(c) ?? 0 };
}

export function emptyCheck(n: number): PolicyCheck {
	return { id: `check-${n}`, metric: "verified_success", condition: "clean", rule: "no_regression" };
}

/**
 * The starter policy a new rule set opens with: the shipped default rules, trimmed. It takes the name
 * of the free file it will be saved as, with no description, so a second set does not look copied (N6).
 */
export function starterPolicy(name = "my-release-policy"): ReleasePolicy {
	return {
		policy_version: 1,
		name,
		description: "",
		checks: [
			{ id: "clean-no-regression", metric: "verified_success", condition: "clean", rule: "no_regression" },
			{ id: "no-integrity-violations", metric: "integrity_violations", condition: "all", equals: 0 },
			{ id: "cost-per-verified-success", metric: "cost_per_verified_success", condition: "clean", max: 2, severity: "warn" },
		],
	};
}

/** Why a rule set name is refused, or null (p14-2). */
export function policyNameProblem(name: string): string | null {
	return idProblem(name, "rule set name");
}


export interface PolicyFormProps {
	value: ReleasePolicy;
	onChange(v: ReleasePolicy): void;
	disabled?: boolean;
}

/** An icon per known metric, so each rule row reads at a glance. */
export function metricIcon(metric: string): LucideIcon {
	switch (metric) {
		case "verified_success":
			return CheckCircle2;
		case "false_completion":
			return CircleAlert;
		case "integrity_violations":
			return ShieldAlert;
		case "retry_amplification":
			return Repeat;
		case "cost_per_verified_success":
		case "cost_usd":
			return DollarSign;
		case "latency_s":
			return Timer;
		default:
			return Gauge;
	}
}

/** Click-first editor of a release policy: name, description, on_unknown and a list of rule rows. */
export function PolicyForm({ value, onChange, disabled }: PolicyFormProps) {
	const { t } = useTranslation("govern");
	const { t: tu } = useTranslation("u14");
	const setCheck = (i: number, c: PolicyCheck) => onChange({ ...value, checks: value.checks.map((x, j) => (j === i ? c : x)) });
	// p14-2: the same rule as the server's name pattern, shown before Save.
	const nameProblem = policyNameProblem(value.name);
	return (
		<fieldset className="flex flex-col gap-6" disabled={disabled} data-testid="policy-form">
			<div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
				<label className="flex flex-col gap-1.5 text-sm font-medium">
					{t("policies.name")}
					<Input
						value={value.name}
						onChange={(e) => onChange({ ...value, name: e.target.value })}
						aria-invalid={nameProblem !== null}
						aria-describedby={nameProblem ? "policy-name-problem" : undefined}
						data-testid="policy-name"
					/>
					{nameProblem && (
						<span id="policy-name-problem" className="text-destructive text-xs font-normal" data-testid="policy-name-problem">
							{nameProblem}
						</span>
					)}
				</label>
				<label className="flex flex-col gap-1.5 text-sm font-medium">
					{t("policies.onUnknown")}
					<NativeSelect
						value={value.on_unknown ?? ""}
						onChange={(e) => onChange({ ...value, on_unknown: e.target.value as ReleasePolicy["on_unknown"] })}
					>
						<option value="">{t("policies.onUnknownDefault")}</option>
						<option value="block">{t("policies.onUnknownBlock")}</option>
						<option value="warn">{t("policies.onUnknownWarn")}</option>
					</NativeSelect>
				</label>
				<label className="flex flex-col gap-1.5 text-sm font-medium sm:col-span-2">
					{t("policies.description")}
					<Input value={value.description ?? ""} onChange={(e) => onChange({ ...value, description: e.target.value })} />
				</label>
			</div>
			<div className="flex flex-col gap-3">
				<div className="flex items-center justify-between">
					<h3 className="text-sm font-semibold">{tu("policies.rulesTitle")}</h3>
					<Button
						type="button"
						size="sm"
						variant="outline"
						onClick={() => onChange({ ...value, checks: [...value.checks, emptyCheck(value.checks.length + 1)] })}
						data-testid="policy-add-check"
					>
						<Plus className="size-4" aria-hidden="true" />
						{t("policies.addCheck")}
					</Button>
				</div>
				{value.checks.length === 0 && <p className="text-muted-foreground text-xs">{t("policies.needCheck")}</p>}
				<ol className="flex flex-col gap-2">
					{value.checks.map((c, i) => (
						<CheckRow
							key={i}
							index={i}
							check={c}
							onChange={(nc) => setCheck(i, nc)}
							onRemove={() => onChange({ ...value, checks: value.checks.filter((_, j) => j !== i) })}
						/>
					))}
				</ol>
			</div>
			<datalist id="policy-metrics">
				{POLICY_METRICS.map((m) => (
					<option key={m} value={m} label={String(tu(`policies.metricLabel.${m}` as never))} />
				))}
			</datalist>
		</fieldset>
	);
}

/** A plain-language metric name, falling back to the raw metric for custom ones. */
export function metricLabel(metric: string, tu: TFunction<"u14">): string {
	return POLICY_METRICS.includes(metric) ? String(tu(`policies.metricLabel.${metric}` as never)) : metric;
}

/** One rule row as a plain sentence, e.g. "Block the release if share of tasks solved gets worse than the current version." */
export function checkSentence(c: PolicyCheck, tu: TFunction<"u14">): string {
	const kind = checkKind(c);
	const action = String(tu(`policies.sentenceAction.${c.severity === "warn" ? "warn" : "block"}` as never));
	return String(
		tu(`policies.sentence.${kind}` as never, { action, metric: metricLabel(c.metric, tu), value: thresholdOf(c) ?? 0 } as never),
	);
}

const chipSelect = "border-input bg-background h-8 rounded-md border px-2 text-xs";

function CheckRow({
	index,
	check,
	onChange,
	onRemove,
}: {
	index: number;
	check: PolicyCheck;
	onChange(c: PolicyCheck): void;
	onRemove(): void;
}) {
	const { t } = useTranslation("govern");
	const { t: tu } = useTranslation("u14");
	const [advanced, setAdvanced] = useState(false);
	const kind = checkKind(check);
	const numeric = kind === "max" || kind === "min" || kind === "equals";
	const warn = check.severity === "warn";
	const Icon = metricIcon(check.metric);
	return (
		<li
			className={cn(
				"bg-card group flex flex-col gap-3 rounded-xl border border-l-4 p-3 transition hover:shadow-sm",
				warn ? "border-l-chart-warning" : "border-l-chart-error",
			)}
			data-testid={`policy-check-${index}`}
		>
			<div className="flex flex-wrap items-center gap-2 text-xs">
				<span className="bg-muted text-muted-foreground flex size-8 shrink-0 items-center justify-center rounded-lg">
					<Icon className="size-4" aria-hidden="true" />
				</span>
				<span className="text-muted-foreground">{tu("policies.when")}</span>
				{/* M7-69: the metric is picked from the known list, never typed; an unknown saved one stays shown. */}
				<select
					className={cn(chipSelect, "max-w-full min-w-0")}
					aria-label={t("policies.metric")}
					title={metricLabel(check.metric, tu)}
					value={check.metric}
					onChange={(e) => onChange({ ...check, metric: e.target.value })}
					data-testid={`policy-check-metric-${index}`}
				>
					{!check.metric && <option value="">{t("policies.metric")}</option>}
					{check.metric && !(POLICY_METRICS as readonly string[]).includes(check.metric) && <option value={check.metric}>{check.metric}</option>}
					{POLICY_METRICS.map((m) => (
						<option key={m} value={m}>
							{metricLabel(m, tu)}
						</option>
					))}
				</select>
				<select
					className={chipSelect}
					aria-label={t("policies.kind")}
					value={kind}
					onChange={(e) => onChange(withKind(check, e.target.value as CheckKind))}
					data-testid={`policy-check-kind-${index}`}
				>
					<option value="no_regression">{t("policies.kindNoRegression")}</option>
					<option value="improved">{t("policies.kindImproved")}</option>
					<option value="max">{t("policies.kindMax")}</option>
					<option value="min">{t("policies.kindMin")}</option>
					<option value="equals">{t("policies.kindEquals")}</option>
				</select>
				{numeric && (
					<Input
						type="number"
						step="any"
						aria-label={t("policies.threshold")}
						className="h-8 w-24 text-xs"
						value={thresholdOf(check) ?? 0}
						onChange={(e) => onChange({ ...check, [kind]: Number(e.target.value) })}
						data-testid={`policy-check-threshold-${index}`}
					/>
				)}
				<select
					className={chipSelect}
					aria-label={t("policies.condition")}
					title={t("policies.conditionHint")}
					value={check.condition ?? ""}
					onChange={(e) => onChange({ ...check, condition: e.target.value || undefined })}
					data-testid={`policy-check-condition-${index}`}
				>
					{POLICY_CONDITIONS.map((o) => (
						<option key={o.key} value={o.value}>
							{t(`policies.${o.key}` as never)}
						</option>
					))}
					{!isListedCondition(check.condition) && (
						<option value={check.condition}>{t("policies.conditionCustom", { id: check.condition })}</option>
					)}
				</select>
				<span className="text-muted-foreground">{tu("policies.then")}</span>
				<select
					className={cn(chipSelect, warn ? "text-chart-warning-ink" : "text-chart-error-ink")}
					aria-label={t("policies.severity")}
					value={check.severity || "block"}
					onChange={(e) => onChange({ ...check, severity: e.target.value === "warn" ? "warn" : undefined })}
				>
					<option value="block">{t("policies.severityBlock")}</option>
					<option value="warn">{t("policies.severityWarn")}</option>
				</select>
				<span className="ml-auto flex items-center gap-1">
					<Button
						type="button"
						size="sm"
						variant="ghost"
						className="h-8 px-2"
						aria-expanded={advanced}
						aria-label={tu("policies.advanced")}
						title={tu("policies.advanced")}
						onClick={() => setAdvanced((a) => !a)}
					>
						<SlidersHorizontal className="size-4" aria-hidden="true" />
					</Button>
					<Button
						type="button"
						size="sm"
						variant="ghost"
						className="text-muted-foreground hover:text-destructive h-8 px-2"
						onClick={onRemove}
						aria-label={t("policies.removeCheck")}
						title={t("policies.removeCheck")}
						data-testid={`policy-check-remove-${index}`}
					>
						<Trash2 className="size-4" aria-hidden="true" />
					</Button>
				</span>
			</div>
			<p className="text-muted-foreground pl-10 text-xs" data-testid={`policy-check-sentence-${index}`}>
				{checkSentence(check, tu)}
			</p>
			{advanced && (
				<div className="grid grid-cols-1 gap-3 border-t pt-3 sm:grid-cols-2">
					<label className="text-muted-foreground flex flex-col gap-1 text-xs">
						{t("policies.checkId")}
						<Input className="h-8 text-xs" value={check.id} onChange={(e) => onChange({ ...check, id: e.target.value })} />
					</label>
					<label className="text-muted-foreground flex flex-col gap-1 text-xs">
						{t("policies.minN")}
						<Input
							className="h-8 text-xs"
							type="number"
							min={0}
							value={check.min_n ?? ""}
							onChange={(e) => onChange({ ...check, min_n: e.target.value === "" ? undefined : Number(e.target.value) })}
						/>
					</label>
				</div>
			)}
		</li>
	);
}