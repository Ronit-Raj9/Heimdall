import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { readWriteToken, saveWriteToken } from "@/lib/store/apis/benchApi";
import type { CalibrationItem, CalibrationLabelRequest, CalibrationStatus, JudgeCalibrationVerdict, Mode } from "@/lib/types/bench";
import { useId, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import {
	EMPTY_LABEL_FORM,
	canLabel,
	formatRate,
	fprBound,
	labelFormErrors,
	labelRequestFrom,
	stateTone,
	type LabelFormValues,
} from "./calibration.utils";

// W8 judge calibration (docs/judge-calibration.md): the calibration card of a judged suite, the
// labelling form, the label list and the contested badge.

const TONE_VARIANT = { good: "success", warn: "warning", bad: "destructive", neutral: "secondary" } as const;

/** The state of a judge as a badge. */
export function CalibrationStateBadge({ state }: { state: CalibrationStatus["state"] }) {
	const { t } = useTranslation("bench");
	return (
		<Badge variant={TONE_VARIANT[stateTone(state)]} data-testid="calibration-state">
			{t(`calibration.state.${state}`)}
		</Badge>
	);
}

/** "N contested": judged runs whose verdict is in dispute (split votes, auditor, a review). */
export function ContestedBadge({ count }: { count: number }) {
	const { t } = useTranslation("bench");
	if (count <= 0) return null;
	return (
		<Badge variant="warning" data-testid="calibration-contested">
			{t("calibration.contested", { count })}
		</Badge>
	);
}

function Row({ label, children, testId }: { label: string; children: ReactNode; testId: string }) {
	return (
		<div className="flex flex-wrap items-baseline justify-between gap-2 border-b py-1.5 text-sm last:border-b-0" data-testid={testId}>
			<dt className="text-muted-foreground">{label}</dt>
			<dd className="text-foreground font-mono text-xs">{children}</dd>
		</div>
	);
}

/** The calibration card: state, agreement, kappa, false-positive bound, set size, date, reminders. */
export function CalibrationCard({ status }: { status: CalibrationStatus }) {
	const { t } = useTranslation("bench");
	const rec = status.latest;
	const h = rec?.human;
	return (
		<div className="bg-card flex flex-col gap-3 rounded-md border p-4" data-testid="calibration-card">
			<div className="flex flex-wrap items-center gap-2">
				<h2 className="text-foreground text-[15px] font-semibold">{t("calibration.cardTitle", { suite: status.suite })}</h2>
				<CalibrationStateBadge state={status.state} />
			</div>
			<p className="text-muted-foreground text-sm break-words" data-testid="calibration-detail">
				{status.detail}
			</p>
			{status.judged && (
				<p className="text-muted-foreground text-xs" data-testid="calibration-judge">
					{t("calibration.judge", { provider: status.provider, model: status.model, prompt: status.prompt_version })}
				</p>
			)}
			<dl className="flex flex-col">
				<Row label={t("calibration.agreement")} testId="calibration-agreement">
					{formatRate(h?.agreement)}
				</Row>
				<Row label={t("calibration.kappa")} testId="calibration-kappa">
					{h?.kappa === null || h?.kappa === undefined ? "n/a" : h.kappa.toFixed(2)}
				</Row>
				<Row label={t("calibration.tnr")} testId="calibration-tnr">
					{formatRate(h?.tnr)}
				</Row>
				<Row label={t("calibration.fprBound")} testId="calibration-fpr">
					{fprBound(h?.fpr)}
				</Row>
				<Row label={t("calibration.setSize")} testId="calibration-set-size">
					{t("calibration.setSizeValue", {
						human: status.human_labels,
						seeds: status.seed_items,
						disputed: status.disputed,
						rows: status.labels,
					})}
				</Row>
				<Row label={t("calibration.date")} testId="calibration-date">
					{rec ? `${rec.created_at.slice(0, 10)} (${rec.record_id})` : t("calibration.never")}
				</Row>
				<Row label={t("calibration.thresholds")} testId="calibration-thresholds">
					{t("calibration.thresholdsValue", {
						agreement: status.thresholds.min_agreement.toFixed(2),
						tnr: status.thresholds.min_tnr.toFixed(2),
						labels: status.thresholds.min_labels,
					})}
				</Row>
			</dl>
			{status.reminders.length > 0 && (
				<ul className="text-chart-warning-ink flex list-disc flex-col gap-1 pl-5 text-xs" data-testid="calibration-reminders">
					{status.reminders.map((r) => (
						<li key={r}>{r}</li>
					))}
				</ul>
			)}
			{status.problems.length > 0 && (
				<ul className="text-chart-error-ink flex list-disc flex-col gap-1 pl-5 text-xs" data-testid="calibration-problems">
					{status.problems.map((p) => (
						<li key={p}>{p}</li>
					))}
				</ul>
			)}
			<p className="text-muted-foreground text-xs">{t("calibration.runHint", { suite: status.suite })}</p>
		</div>
	);
}

/** The judge_calibration result of a verdict: state, contested runs, capped decision, bias-adjusted rates. */
export function VerdictCalibration({ jc }: { jc: JudgeCalibrationVerdict }) {
	const { t } = useTranslation("bench");
	const arms = Object.keys(jc.bias_adjusted ?? {}).sort();
	return (
		<div className="flex flex-col gap-2 rounded-md border p-3" data-testid="verdict-calibration">
			<div className="flex flex-wrap items-center gap-2">
				<CalibrationStateBadge state={jc.state} />
				<ContestedBadge count={jc.contested_runs} />
				{jc.capped_from && (
					<Badge variant="destructive" data-testid="calibration-capped">
						{t("calibration.capped", { decision: jc.capped_from.toUpperCase() })}
					</Badge>
				)}
			</div>
			<p className="text-muted-foreground text-sm break-words">{jc.detail}</p>
			{jc.error_over_effect && (
				<p className="text-chart-warning-ink text-sm" data-testid="calibration-error-over-effect">
					{jc.error_over_effect}
				</p>
			)}
			{arms.length > 0 && (
				<table className="w-full text-xs" data-testid="calibration-bias-adjusted">
					<thead>
						<tr className="text-muted-foreground text-left">
							<th className="py-1 font-medium">{t("calibration.arm")}</th>
							<th className="py-1 font-medium">{t("calibration.raw")}</th>
							<th className="py-1 font-medium">{t("calibration.corrected")}</th>
						</tr>
					</thead>
					<tbody>
						{arms.map((arm) => {
							const r = jc.bias_adjusted![arm];
							const corrected =
								r.estimate === null
									? "n/a"
									: `${r.estimate.toFixed(3)}${r.lo !== null && r.hi !== null ? ` [${r.lo.toFixed(3)}, ${r.hi.toFixed(3)}]` : ""}`;
							return (
								<tr key={arm} className="border-t" data-testid={`calibration-bias-${arm}`}>
									<td className="py-1 font-mono">{arm}</td>
									<td className="py-1 font-mono">{formatRate(r.raw)}</td>
									<td className="py-1 font-mono" title={r.note}>
										{corrected}
									</td>
								</tr>
							);
						})}
					</tbody>
				</table>
			)}
			<p className="text-muted-foreground text-xs">{t("calibration.reportOnly")}</p>
		</div>
	);
}

export interface LabelFormProps {
	tasks: readonly string[];
	mode: Mode | undefined;
	submitting?: boolean;
	submitError?: string;
	initial?: LabelFormValues;
	onSubmit: (req: CalibrationLabelRequest) => void;
}

/** The labelling view: task, answer, correct or incorrect, labeller, note, behind the write token. */
export function LabelForm({ tasks, mode, submitting, submitError, initial, onSubmit }: LabelFormProps) {
	const { t } = useTranslation("bench");
	const ids = useId();
	const [values, setValues] = useState<LabelFormValues>(initial ?? EMPTY_LABEL_FORM);
	const [showErrors, setShowErrors] = useState(false);
	const [token, setToken] = useState(readWriteToken);
	const writable = canLabel(mode);
	const errors = showErrors ? labelFormErrors(values) : {};
	const set = (k: keyof LabelFormValues) => (v: string) => setValues((cur) => ({ ...cur, [k]: v }));
	const field = "border-input bg-background w-full rounded-md border px-2 py-1.5 text-sm";
	return (
		<form
			className="flex flex-col gap-3 rounded-md border p-4"
			data-testid="calibration-label-form"
			onSubmit={(e) => {
				e.preventDefault();
				const req = labelRequestFrom(values);
				if (!req) {
					setShowErrors(true);
					return;
				}
				onSubmit(req);
			}}
		>
			<h2 className="text-foreground text-[15px] font-semibold">{t("calibration.labelTitle")}</h2>
			{!writable && (
				<p className="text-muted-foreground text-xs" data-testid="calibration-label-readonly">
					{t("calibration.readOnly", { mode: mode ?? "?" })}
				</p>
			)}
			<label className="flex flex-col gap-1 text-sm" htmlFor={`${ids}-task`}>
				{t("calibration.task")}
				<select
					id={`${ids}-task`}
					className={field}
					value={values.task}
					onChange={(e) => set("task")(e.target.value)}
					disabled={!writable}
					aria-invalid={errors.task ? true : undefined}
					data-testid="calibration-label-task"
				>
					<option value="">{t("calibration.pickTask")}</option>
					{tasks.map((id) => (
						<option key={id} value={id}>
							{id}
						</option>
					))}
				</select>
			</label>
			<label className="flex flex-col gap-1 text-sm" htmlFor={`${ids}-answer`}>
				{t("calibration.answer")}
				<textarea
					id={`${ids}-answer`}
					className={`${field} min-h-24 font-mono`}
					value={values.answer}
					onChange={(e) => set("answer")(e.target.value)}
					disabled={!writable}
					aria-invalid={errors.answer ? true : undefined}
					data-testid="calibration-label-answer"
				/>
			</label>
			<fieldset className="flex flex-wrap gap-4 text-sm" disabled={!writable} data-testid="calibration-label-choice">
				<legend className="mb-1">{t("calibration.label")}</legend>
				{(["correct", "incorrect"] as const).map((l) => (
					<label key={l} className="flex items-center gap-1.5">
						<input
							type="radio"
							name={`${ids}-label`}
							value={l}
							checked={values.label === l}
							onChange={() => set("label")(l)}
							data-testid={`calibration-label-${l}`}
						/>
						{t(`calibration.labels.${l}`)}
					</label>
				))}
				{errors.label && (
					<span className="text-chart-error-ink text-xs" data-testid="calibration-label-error-label">
						{t("calibration.pickLabel")}
					</span>
				)}
			</fieldset>
			<label className="flex flex-col gap-1 text-sm" htmlFor={`${ids}-labeller`}>
				{t("calibration.labeller")}
				<Input
					id={`${ids}-labeller`}
					value={values.labeller}
					onChange={(e) => set("labeller")(e.target.value)}
					disabled={!writable}
					aria-invalid={errors.labeller ? true : undefined}
					data-testid="calibration-label-labeller"
				/>
			</label>
			<label className="flex flex-col gap-1 text-sm" htmlFor={`${ids}-note`}>
				{t("calibration.note")}
				<Input
					id={`${ids}-note`}
					value={values.note}
					onChange={(e) => set("note")(e.target.value)}
					disabled={!writable}
					data-testid="calibration-label-note"
				/>
			</label>
			<label className="flex flex-col gap-1 text-sm" htmlFor={`${ids}-token`}>
				{t("calibration.token")}
				<Input
					id={`${ids}-token`}
					type="password"
					autoComplete="off"
					value={token}
					onChange={(e) => {
						setToken(e.target.value);
						saveWriteToken(e.target.value);
					}}
					disabled={!writable}
					data-testid="calibration-token-input"
				/>
			</label>
			<div>
				<Button type="submit" size="sm" disabled={!writable || submitting} dataTestId="calibration-label-submit">
					{t("calibration.submit")}
				</Button>
			</div>
			{showErrors && Object.keys(errors).length > 0 && (
				<p className="text-chart-error-ink text-xs" data-testid="calibration-label-errors">
					{t("calibration.incomplete")}
				</p>
			)}
			{submitError && (
				<p role="alert" className="text-chart-error-ink text-sm break-words" data-testid="calibration-label-submit-error">
					{submitError}
				</p>
			)}
		</form>
	);
}

/** Every label row, newest first, the answer shortened. */
export function LabelsTable({ items }: { items: readonly CalibrationItem[] }) {
	const { t } = useTranslation("bench");
	if (items.length === 0) {
		return (
			<p className="text-muted-foreground text-sm" data-testid="calibration-labels-empty">
				{t("calibration.noLabels")}
			</p>
		);
	}
	const rows = [...items].reverse();
	return (
		<table className="w-full text-xs" data-testid="calibration-labels">
			<thead>
				<tr className="text-muted-foreground text-left">
					<th className="py-1 font-medium">{t("calibration.task")}</th>
					<th className="py-1 font-medium">{t("calibration.answer")}</th>
					<th className="py-1 font-medium">{t("calibration.label")}</th>
					<th className="py-1 font-medium">{t("calibration.labeller")}</th>
					<th className="py-1 font-medium">{t("calibration.source")}</th>
				</tr>
			</thead>
			<tbody>
				{rows.map((it, i) => (
					<tr key={`${it.id}-${it.labeller}-${it.source}-${i}`} className="border-t" data-testid="calibration-label-row">
						<td className="py-1 font-mono">{it.task}</td>
						<td className="max-w-xs truncate py-1" title={it.answer}>
							{it.answer.length > 80 ? `${it.answer.slice(0, 80)}...` : it.answer}
						</td>
						<td className="py-1">{t(`calibration.labels.${it.label}`)}</td>
						<td className="py-1">{it.labeller}</td>
						<td className="py-1">
							{it.source === "admission_control" ? (
								<Badge variant="secondary" data-testid="calibration-seed">
									{t("calibration.seed")}
								</Badge>
							) : (
								t(`calibration.sources.${it.source}`)
							)}
						</td>
					</tr>
				))}
			</tbody>
		</table>
	);
}