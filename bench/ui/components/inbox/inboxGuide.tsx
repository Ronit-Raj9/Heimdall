import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { InboxSession, InboxTaskKind } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Plus, Trash2 } from "lucide-react";
import { useId, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { isToolKind, parseArgs, type GuideCallRow, type GuideFormError, type GuideFormValues } from "./inboxGuide.utils";
import { ProposalBar } from "./proposalBar";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		inboxGuide: typeof import("@/locales/en/inboxGuide.json");
	}
}

const FIELD =
	"placeholder:text-muted-foreground/70 dark:bg-input/30 border-input focus-visible:border-primary bg-card w-full min-w-0 rounded-md border px-3 py-2 text-sm shadow-xs outline-none disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive";

const VERDICTS: readonly GuideCallRow["verdict"][] = ["right", "wrong", "skip"];

export interface GuideFieldsProps {
	kind: InboxTaskKind;
	session?: InboxSession;
	golden?: boolean;
	values: GuideFormValues;
	errors: readonly GuideFormError[];
	disabled?: boolean;
	onChange: (patch: Partial<GuideFormValues>) => void;
	/** The inbox item and the benchmark it joins: with both, the review bar (draft with a model, approve) is shown on top. */
	itemId?: string;
	suite?: string;
	/** Scrolls the session panel to a quoted turn (seq 0) or call (seq n). */
	onQuote?: (seq: number) => void;
}

/**
 * "What should have happened?": the person says the right answer, or which recorded calls were
 * right and which were wrong (and adds a missing one), so the accept writes a finished task with
 * its checks, reference and mutants instead of a draft to finish by hand.
 */
export function GuideFields({ kind, session, golden, values, errors, disabled, onChange, itemId, suite, onQuote }: GuideFieldsProps) {
	const { t } = useTranslation("inboxGuide");
	const ids = useId();
	const tools = isToolKind(kind);
	const requests = session?.requests ?? [];
	const setCall = (i: number, patch: Partial<GuideCallRow>) =>
		onChange({ calls: values.calls.map((c, j) => (j === i ? { ...c, ...patch } : c)) });

	return (
		<fieldset className="border-border flex flex-col gap-3 rounded-md border px-3 py-3" disabled={disabled} data-testid="inbox-guide">
			<legend className="px-1 text-sm font-medium">{t("title")}</legend>
			{itemId && suite && <ProposalBar itemId={itemId} suite={suite} kind={kind} values={values} onChange={onChange} onQuote={onQuote} />}
			<div role="radiogroup" aria-label={t("mode.label")} className="flex flex-wrap gap-2" data-testid="inbox-guide-mode">
				{(["guided", "draft"] as const).map((m) => (
					<Button
						key={m}
						type="button"
						size="sm"
						variant={values.mode === m ? "default" : "outline"}
						role="radio"
						aria-checked={values.mode === m}
						disabled={disabled || (m === "guided" && !session)}
						onClick={() => onChange({ mode: m })}
						dataTestId={`inbox-guide-mode-${m}`}
					>
						{t(`mode.${m}`)}
					</Button>
				))}
			</div>
			<p className="text-muted-foreground text-xs">{values.mode === "guided" ? t("mode.guidedHint") : t("mode.draftHint")}</p>
			{values.mode === "guided" && (
				<>
					{golden && (
						<p className="bg-muted rounded-sm px-3 py-2 text-xs" role="note" data-testid="inbox-guide-golden">
							{t("golden")}
						</p>
					)}
					<Field label={t("instruction.label")} htmlFor={`${ids}-instr`} hint={t("instruction.hint")}>
						<textarea
							id={`${ids}-instr`}
							className={cn(FIELD, "min-h-20 font-mono text-xs")}
							placeholder={requests[0] ?? ""}
							value={values.instruction}
							onChange={(e) => onChange({ instruction: e.target.value })}
							data-testid="inbox-guide-instruction"
						/>
					</Field>
					{requests.length > 1 && (
						<Check
							checked={values.keepTurns}
							onChange={(v) => onChange({ keepTurns: v })}
							label={t("keepTurns", { count: requests.length - 1 })}
							testId="inbox-guide-keep-turns"
						/>
					)}
					{tools ? (
						<CallsEditor values={values} errors={errors} onChange={onChange} setCall={setCall} />
					) : (
						<>
							<Field label={t("answers.label")} htmlFor={`${ids}-answers`} hint={t("answers.hint")}>
								<textarea
									id={`${ids}-answers`}
									className={cn(FIELD, "min-h-20")}
									value={values.answers}
									onChange={(e) => onChange({ answers: e.target.value })}
									aria-invalid={errors.includes("answers") || undefined}
									data-testid="inbox-guide-answers"
								/>
								{errors.includes("answers") && <FieldError testId="inbox-guide-error-answers">{t("answers.required")}</FieldError>}
							</Field>
							{session?.final_answer && (
								<div className="flex flex-col gap-1">
									<p className="text-muted-foreground text-xs">{t("answers.recorded")}</p>
									<pre
										className="bg-muted max-h-32 overflow-auto rounded-sm px-2 py-1 text-xs whitespace-pre-wrap"
										data-testid="inbox-guide-recorded"
									>
										{session.final_answer}
									</pre>
									<Check
										checked={values.recordedWrong}
										onChange={(v) => onChange({ recordedWrong: v })}
										label={t("answers.recordedWrong")}
										testId="inbox-guide-recorded-wrong"
									/>
								</div>
							)}
						</>
					)}
				</>
			)}
		</fieldset>
	);
}

function CallsEditor({
	values,
	errors,
	onChange,
	setCall,
}: {
	values: GuideFormValues;
	errors: readonly GuideFormError[];
	onChange: (patch: Partial<GuideFormValues>) => void;
	setCall: (i: number, patch: Partial<GuideCallRow>) => void;
}) {
	const { t } = useTranslation("inboxGuide");
	const ids = useId();
	return (
		<div className="flex flex-col gap-2">
			<p className="text-muted-foreground text-xs">{t("calls.hint")}</p>
			{values.calls.length === 0 && <p className="text-muted-foreground text-xs">{t("calls.none")}</p>}
			<ol className="flex flex-col gap-2" data-testid="inbox-guide-calls">
				{values.calls.map((call, i) => {
					const badArgs = call.verdict !== "skip" && !parseArgs(call.args).ok;
					return (
						<li key={i} className="border-border flex flex-col gap-1.5 rounded-sm border px-2 py-2" data-testid={`inbox-guide-call-${i}`}>
							<div className="flex flex-wrap items-center gap-2">
								<span className="text-muted-foreground w-5 text-xs tabular-nums">{i + 1}.</span>
								{call.added ? (
									<Input
										className="h-8 w-48 font-mono text-xs"
										aria-label={t("calls.tool")}
										placeholder={t("calls.toolPlaceholder")}
										value={call.tool}
										onChange={(e) => setCall(i, { tool: e.target.value })}
										aria-invalid={(call.verdict !== "skip" && call.tool.trim() === "") || undefined}
										data-testid={`inbox-guide-call-tool-${i}`}
									/>
								) : (
									<code className="font-mono text-xs break-all">{call.tool}</code>
								)}
								<div
									role="radiogroup"
									aria-label={t("calls.verdict", { tool: call.tool || t("calls.toolPlaceholder") })}
									className="ml-auto flex gap-1"
								>
									{VERDICTS.map((v) => (
										<Button
											key={v}
											type="button"
											size="sm"
											variant={call.verdict === v ? (v === "wrong" ? "destructive" : "default") : "outline"}
											role="radio"
											aria-checked={call.verdict === v}
											onClick={() => setCall(i, { verdict: v })}
											dataTestId={`inbox-guide-call-${i}-${v}`}
										>
											{t(`calls.${v}`)}
										</Button>
									))}
									{call.added && (
										<Button
											type="button"
											size="sm"
											variant="ghost"
											aria-label={t("calls.remove")}
											onClick={() => onChange({ calls: values.calls.filter((_, j) => j !== i) })}
											dataTestId={`inbox-guide-call-remove-${i}`}
										>
											<Trash2 className="size-3.5" aria-hidden="true" />
										</Button>
									)}
								</div>
							</div>
							<Input
								className="h-8 font-mono text-xs"
								aria-label={t("calls.args", { tool: call.tool })}
								placeholder={t("calls.argsPlaceholder")}
								value={call.args}
								onChange={(e) => setCall(i, { args: e.target.value })}
								aria-invalid={badArgs || undefined}
								data-testid={`inbox-guide-call-args-${i}`}
							/>
						</li>
					);
				})}
			</ol>
			{errors.includes("args") && <FieldError testId="inbox-guide-error-args">{t("calls.badArgs")}</FieldError>}
			{errors.includes("tool") && <FieldError testId="inbox-guide-error-tool">{t("calls.toolRequired")}</FieldError>}
			{errors.includes("calls") && <FieldError testId="inbox-guide-error-calls">{t("calls.required")}</FieldError>}
			<div>
				<Button
					type="button"
					size="sm"
					variant="outline"
					onClick={() => onChange({ calls: [...values.calls, { tool: "", args: "", verdict: "right", added: true }] })}
					dataTestId="inbox-guide-add-call"
				>
					<Plus className="size-3.5" aria-hidden="true" />
					{t("calls.add")}
				</Button>
			</div>
			{values.calls.filter((c) => c.verdict === "right").length > 1 && (
				<Check
					checked={values.ordered}
					onChange={(v) => onChange({ ordered: v })}
					label={t("calls.ordered")}
					testId="inbox-guide-ordered"
				/>
			)}
			<Field label={t("rubric.label")} htmlFor={`${ids}-rubric`} hint={t("rubric.hint")}>
				<textarea
					id={`${ids}-rubric`}
					className={cn(FIELD, "min-h-16")}
					value={values.replyRubric}
					onChange={(e) => onChange({ replyRubric: e.target.value })}
					data-testid="inbox-guide-rubric"
				/>
			</Field>
			<Check
				checked={values.switchGrader}
				onChange={(v) => onChange({ switchGrader: v })}
				label={t("switchGrader.label")}
				hint={t("switchGrader.hint")}
				testId="inbox-guide-switch-grader"
			/>
		</div>
	);
}

function Check({
	checked,
	onChange,
	label,
	hint,
	testId,
}: {
	checked: boolean;
	onChange: (v: boolean) => void;
	label: string;
	hint?: string;
	testId: string;
}) {
	const id = useId();
	return (
		<div className="flex flex-col gap-0.5">
			<label htmlFor={id} className="flex items-start gap-2 text-sm">
				<input
					id={id}
					type="checkbox"
					className="mt-1"
					checked={checked}
					onChange={(e) => onChange(e.target.checked)}
					data-testid={testId}
				/>
				<span>{label}</span>
			</label>
			{hint && <p className="text-muted-foreground pl-6 text-xs">{hint}</p>}
		</div>
	);
}

function Field({ label, htmlFor, hint, children }: { label: string; htmlFor: string; hint?: string; children: ReactNode }) {
	return (
		<div className="flex flex-col gap-1">
			<label htmlFor={htmlFor} className="text-muted-foreground text-xs">
				{label}
			</label>
			{children}
			{hint && <p className="text-muted-foreground text-xs">{hint}</p>}
		</div>
	);
}

function FieldError({ children, testId }: { children: ReactNode; testId: string }) {
	return (
		<p className="text-chart-error-ink text-xs" role="alert" data-testid={testId}>
			{children}
		</p>
	);
}