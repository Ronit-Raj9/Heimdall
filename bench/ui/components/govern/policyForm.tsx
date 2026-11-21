import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { PolicyCheck, ReleasePolicy } from "@/lib/types/govern";
import { Plus, Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";

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

/** The starter policy a first-time user creates: the shipped default rules, trimmed. */
export function starterPolicy(): ReleasePolicy {
	return {
		policy_version: 1,
		name: "my-release-policy",
		description: "No regression on clean runs, no integrity violations, a cost ceiling that warns.",
		checks: [
			{ id: "clean-no-regression", metric: "verified_success", condition: "clean", rule: "no_regression" },
			{ id: "no-integrity-violations", metric: "integrity_violations", condition: "all", equals: 0 },
			{ id: "cost-per-verified-success", metric: "cost_per_verified_success", condition: "clean", max: 2, severity: "warn" },
		],
	};
}

const selectClass = "border-input bg-background h-9 rounded-md border px-2 text-sm";

export interface PolicyFormProps {
	value: ReleasePolicy;
	onChange(v: ReleasePolicy): void;
	disabled?: boolean;
}

/** Click-first editor of a release policy: name, description, on_unknown and a list of checks. */
export function PolicyForm({ value, onChange, disabled }: PolicyFormProps) {
	const { t } = useTranslation("govern");
	const setCheck = (i: number, c: PolicyCheck) => onChange({ ...value, checks: value.checks.map((x, j) => (j === i ? c : x)) });
	return (
		<fieldset className="flex flex-col gap-4" disabled={disabled} data-testid="policy-form">
			<label className="flex flex-col gap-1 text-sm">
				{t("policies.name")}
				<Input value={value.name} onChange={(e) => onChange({ ...value, name: e.target.value })} data-testid="policy-name" />
			</label>
			<label className="flex flex-col gap-1 text-sm">
				{t("policies.description")}
				<Input value={value.description ?? ""} onChange={(e) => onChange({ ...value, description: e.target.value })} />
			</label>
			<label className="flex flex-col gap-1 text-sm">
				{t("policies.onUnknown")}
				<select
					className={selectClass}
					value={value.on_unknown ?? ""}
					onChange={(e) => onChange({ ...value, on_unknown: e.target.value as ReleasePolicy["on_unknown"] })}
				>
					<option value="">{t("policies.onUnknownDefault")}</option>
					<option value="block">{t("policies.onUnknownBlock")}</option>
					<option value="warn">{t("policies.onUnknownWarn")}</option>
				</select>
			</label>
			<div className="flex items-center justify-between">
				<h3 className="text-sm font-medium">{t("policies.checksTitle")}</h3>
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
			<ol className="flex flex-col gap-3">
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
			<datalist id="policy-metrics">
				{POLICY_METRICS.map((m) => (
					<option key={m} value={m} />
				))}
			</datalist>
		</fieldset>
	);
}

function CheckRow({ index, check, onChange, onRemove }: { index: number; check: PolicyCheck; onChange(c: PolicyCheck): void; onRemove(): void }) {
	const { t } = useTranslation("govern");
	const kind = checkKind(check);
	const numeric = kind === "max" || kind === "min" || kind === "equals";
	return (
		<li className="grid grid-cols-1 gap-2 rounded-sm border p-3 sm:grid-cols-2" data-testid={`policy-check-${index}`}>
			<label className="flex flex-col gap-1 text-xs">
				{t("policies.checkId")}
				<Input value={check.id} onChange={(e) => onChange({ ...check, id: e.target.value })} />
			</label>
			<label className="flex flex-col gap-1 text-xs">
				{t("policies.metric")}
				<Input list="policy-metrics" value={check.metric} onChange={(e) => onChange({ ...check, metric: e.target.value })} />
			</label>
			<label className="flex flex-col gap-1 text-xs" title={t("policies.conditionHint")}>
				{t("policies.condition")}
				<Input value={check.condition ?? ""} onChange={(e) => onChange({ ...check, condition: e.target.value || undefined })} />
			</label>
			<label className="flex flex-col gap-1 text-xs">
				{t("policies.kind")}
				<select
					className={selectClass}
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
			</label>
			{numeric && (
				<label className="flex flex-col gap-1 text-xs">
					{t("policies.threshold")}
					<Input
						type="number"
						step="any"
						value={thresholdOf(check) ?? 0}
						onChange={(e) => onChange({ ...check, [kind]: Number(e.target.value) })}
						data-testid={`policy-check-threshold-${index}`}
					/>
				</label>
			)}
			<label className="flex flex-col gap-1 text-xs">
				{t("policies.minN")}
				<Input
					type="number"
					min={0}
					value={check.min_n ?? ""}
					onChange={(e) => onChange({ ...check, min_n: e.target.value === "" ? undefined : Number(e.target.value) })}
				/>
			</label>
			<label className="flex flex-col gap-1 text-xs">
				{t("policies.severity")}
				<select
					className={selectClass}
					value={check.severity || "block"}
					onChange={(e) => onChange({ ...check, severity: e.target.value === "warn" ? "warn" : undefined })}
				>
					<option value="block">{t("policies.severityBlock")}</option>
					<option value="warn">{t("policies.severityWarn")}</option>
				</select>
			</label>
			<div className="flex items-end justify-end sm:col-span-2">
				<Button type="button" size="sm" variant="ghost" onClick={onRemove} data-testid={`policy-check-remove-${index}`}>
					<Trash2 className="size-4" aria-hidden="true" />
					{t("policies.removeCheck")}
				</Button>
			</div>
		</li>
	);
}
