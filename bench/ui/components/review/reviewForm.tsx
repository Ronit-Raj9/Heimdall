import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { readWriteToken, saveWriteToken } from "@/lib/store/apis/benchApi";
import { REVIEW_DECISIONS, type Mode, type Review, type ReviewRequest } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Info } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { EMPTY_REVIEW_FORM, canWriteReviews, reviewFormErrors, reviewRequestFrom, type ReviewFormValues } from "./review.utils";

export interface ReviewFormProps {
	runId: string;
	expId?: string;
	/** The server mode; only live takes reviews, so the form is read-only otherwise. */
	mode?: Mode;
	initialValues?: Partial<ReviewFormValues>;
	submitting?: boolean;
	submitError?: string;
	created?: Review;
	/** Show missing-field errors before the first submit, as after a submit attempt. */
	showErrors?: boolean;
	onSubmit: (request: ReviewRequest) => void;
}

const TEXT_AREA =
	"placeholder:text-muted-foreground/70 dark:bg-input/30 border-input focus-visible:border-primary w-full min-w-0 rounded-sm border bg-transparent px-3 py-2 text-sm shadow-none outline-none disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive";

/**
 * Records a review of one run: a decision, a required plain-text reason, the reviewer's name and
 * what they cited. There is no login, so the form says plainly that the name is self-declared.
 * On a recorded or synthetic server the form stays visible but disabled, with the reason why.
 */
export function ReviewForm(props: ReviewFormProps) {
	const { t } = useTranslation("bench");
	const ids = useId();
	const [values, setValues] = useState<ReviewFormValues>({ ...EMPTY_REVIEW_FORM, ...props.initialValues });
	const [touched, setTouched] = useState(Boolean(props.showErrors));
	const [token, setToken] = useState(readWriteToken);
	const writable = canWriteReviews(props.mode);
	const errors = reviewFormErrors(values);
	const shown = touched ? errors : [];
	const set = (patch: Partial<ReviewFormValues>) => setValues((v) => ({ ...v, ...patch }));
	const disabled = !writable || props.submitting;

	return (
		<form
			className="flex flex-col gap-4"
			data-testid="review-form"
			noValidate
			onSubmit={(e) => {
				e.preventDefault();
				setTouched(true);
				if (!writable || errors.length > 0 || props.submitting) return;
				props.onSubmit(reviewRequestFrom(props.runId, props.expId, values));
			}}
		>
			{!writable && (
				<p
					className="border-border bg-muted flex items-start gap-2 rounded-sm border px-3 py-2 text-sm"
					role="note"
					data-testid="review-form-read-only"
				>
					<Info className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("review.form.readOnly", { mode: props.mode ?? "unknown" })}
				</p>
			)}
			<fieldset className="flex flex-col gap-1" disabled={disabled}>
				<legend className="text-muted-foreground mb-1 text-xs">{t("review.form.decision")}</legend>
				<div className="grid gap-2 sm:grid-cols-2" role="radiogroup" aria-invalid={shown.includes("decision") || undefined}>
					{REVIEW_DECISIONS.map((decision) => (
						<label
							key={decision}
							className={cn(
								"hover:bg-accent flex cursor-pointer items-start gap-2 rounded-sm border px-3 py-2 text-sm",
								values.decision === decision && "border-primary bg-primary/5",
							)}
							data-testid={`review-decision-${decision}`}
						>
							<input
								type="radio"
								name={`${ids}-decision`}
								className="accent-primary mt-0.5"
								value={decision}
								checked={values.decision === decision}
								onChange={() => set({ decision })}
							/>
							<span className="flex flex-col gap-0.5">
								<span className="font-medium">{t(`review.decision.${decision}`)}</span>
								<span className="text-muted-foreground text-xs">{t(`review.decisionHint.${decision}`)}</span>
							</span>
						</label>
					))}
				</div>
				{shown.includes("decision") && <FieldError testId="review-error-decision">{t("review.form.decisionRequired")}</FieldError>}
			</fieldset>

			<Field label={t("review.form.reason")} htmlFor={`${ids}-reason`}>
				<textarea
					id={`${ids}-reason`}
					rows={4}
					className={TEXT_AREA}
					placeholder={t("review.form.reasonPlaceholder")}
					value={values.reason}
					onChange={(e) => set({ reason: e.target.value })}
					disabled={disabled}
					required
					aria-invalid={shown.includes("reason") || undefined}
					data-testid="review-reason-input"
				/>
				{shown.includes("reason") && <FieldError testId="review-error-reason">{t("review.form.reasonRequired")}</FieldError>}
			</Field>

			<Field label={t("review.form.reviewer")} htmlFor={`${ids}-reviewer`} hint={t("review.form.noAuth")}>
				<Input
					id={`${ids}-reviewer`}
					placeholder={t("review.form.reviewerPlaceholder")}
					value={values.reviewer}
					onChange={(e) => set({ reviewer: e.target.value })}
					disabled={disabled}
					required
					aria-invalid={shown.includes("reviewer") || undefined}
					data-testid="review-reviewer-input"
				/>
				{shown.includes("reviewer") && <FieldError testId="review-error-reviewer">{t("review.form.reviewerRequired")}</FieldError>}
			</Field>

			<Field label={t("review.form.evidence")} htmlFor={`${ids}-evidence`}>
				<textarea
					id={`${ids}-evidence`}
					rows={2}
					className={cn(TEXT_AREA, "font-mono text-xs")}
					placeholder={t("review.form.evidencePlaceholder")}
					value={values.evidence}
					onChange={(e) => set({ evidence: e.target.value })}
					disabled={disabled}
					data-testid="review-evidence-input"
				/>
			</Field>

			<Field label={t("review.form.token")} htmlFor={`${ids}-token`} hint={t("review.form.tokenHint")}>
				<Input
					id={`${ids}-token`}
					type="password"
					autoComplete="off"
					placeholder={t("review.form.tokenPlaceholder")}
					value={token}
					onChange={(e) => {
						setToken(e.target.value);
						saveWriteToken(e.target.value);
					}}
					disabled={!writable}
					data-testid="review-token-input"
				/>
			</Field>

			<Button type="submit" disabled={disabled} isLoading={props.submitting} dataTestId="review-submit">
				{t("review.form.submit")}
			</Button>
			{props.submitError && (
				<p role="alert" className="text-chart-error-ink text-sm break-words" data-testid="review-submit-error">
					{t("review.form.error", { error: props.submitError })}
				</p>
			)}
			{props.created && (
				<p className="text-sm" role="status" data-testid="review-created">
					{t("review.form.created", { id: props.created.review_id })}
				</p>
			)}
		</form>
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