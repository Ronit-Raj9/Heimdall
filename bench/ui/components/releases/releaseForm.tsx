import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { RoleNote } from "@/components/platform/accessNotes";
import { canUse, useSession, usesWriteToken } from "@/lib/session/sessionContext";
import { readWriteToken, saveWriteToken } from "@/lib/store/apis/benchApi";
import type { Action, Decision, ExperimentStatus, ReleasePolicyDecision, ReleaseRecord, ReleaseRequest } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { AlertTriangle } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useListEvaluationsQuery } from "@/lib/store/apis/evaluationsApi";
import "@/components/canaries/uf7bI18n";
import { useTranslation } from "react-i18next";
import "@/app/experiments/views/experimentsI18n";
import { humanExperimentName } from "@/components/experiments/experiments.utils";
import {
	EMPTY_RELEASE_FORM,
	canRelease,
	releaseBlockers,
	releaseDisabledReason,
	releaseRequestFrom,
	type ReleaseFormValues,
} from "./releases.utils";

/** The verdict the release would cite. */
export interface ReleaseVerdict {
	analysisId: string;
	decision: Decision;
	action: Action;
}

export interface ReleaseFormProps {
	/** Newest first. */
	experiments: ExperimentStatus[];
	expId: string | undefined;
	onExpChange: (expId: string) => void;
	verdict?: ReleaseVerdict;
	/** Undefined when no policy was evaluated. */
	policy?: ReleasePolicyDecision;
	verdictLoading?: boolean;
	verdictError?: string;
	initialValues?: Partial<ReleaseFormValues>;
	submitting?: boolean;
	submitError?: string;
	created?: ReleaseRecord;
	onSubmit: (request: ReleaseRequest) => void;
}

/** A picker option: the readable name a person knows, then the id in small grey text. */
export function ExperimentOption({ experiment }: { experiment: ExperimentStatus }) {
	const { t } = useTranslation("experimentsUx");
	return (
		<span className="flex min-w-0 items-baseline gap-2" data-testid={`release-experiment-label-${experiment.exp_id}`}>
			<span className="truncate">{humanExperimentName(experiment, (k, o) => String(t(k as never, (o ?? {}) as never)))}</span>
			<span className="text-muted-foreground shrink-0 font-mono text-xs">{experiment.exp_id}</span>
		</span>
	);
}

/**
 * The release panel's form. The button is enabled only when the verdict action is ship and the policy
 * does not block; otherwise an explicit "override with reason" checkbox is the only way through, and
 * the reason is recorded with the release.
 */
export function ReleaseForm(props: ReleaseFormProps) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("u14");
	const { t: m } = useTranslation("uf7b");
	const { experiments, expId, verdict, policy } = props;
	const session = useSession();
	// M7-81: the approver starts as the signed-in person, so it is not retyped (and not lost) each time.
	const [values, setValues] = useState<ReleaseFormValues>(() => ({
		...EMPTY_RELEASE_FORM,
		...(session.user?.email ? { approver: session.user.email } : {}),
		...props.initialValues,
	}));
	const evaluations = useListEvaluationsQuery();
	const promotable = (evaluations.data ?? [])
		.filter((ev) => ev.status === "done" && (ev.spec?.mode === "versions" || ev.spec?.mode === "score"))
		.sort((a, b) => (b.created_at ?? "").localeCompare(a.created_at ?? ""))
		.slice(0, 3);
	const [token, setToken] = useState(readWriteToken);
	const set = (patch: Partial<ReleaseFormValues>) => setValues((v) => ({ ...v, ...patch }));
	const blockers = verdict ? releaseBlockers(verdict.action, policy) : [];
	const enabled = canRelease(Boolean(verdict), verdict?.action, policy, values) && !props.submitting;
	// Why the button is off, in words (N170): never a silent disabled button.
	const disabledReason = props.submitting
		? null
		: releaseDisabledReason({
				expId,
				verdictLoading: Boolean(props.verdictLoading),
				verdictError: props.verdictError,
				verdictLoaded: Boolean(verdict),
				action: verdict?.action,
				policy,
				values,
			});

	// Hosted: releases need the member role; a viewer sees why, a demo visitor sees no form.
	if (!canUse(session, "member")) return session.signedIn ? <RoleNote min="member" testId="release-form-role" /> : null;

	return (
		<form
			className="flex flex-col gap-4"
			data-testid="release-form"
			onSubmit={(e) => {
				e.preventDefault();
				if (!enabled || !expId) return;
				props.onSubmit(releaseRequestFrom(expId, verdict?.analysisId, blockers.length > 0, values));
			}}
		>
			<Field label={t("releases.panel.experiment")} htmlFor="release-experiment">
				<Select value={expId} onValueChange={props.onExpChange} disabled={experiments.length === 0}>
					<SelectTrigger id="release-experiment" className="w-full" data-testid="release-experiment-picker">
						<SelectValue placeholder={t("releases.panel.experimentPlaceholder")} />
					</SelectTrigger>
					<SelectContent>
						{experiments.map((e) => (
							<SelectItem key={e.exp_id} value={e.exp_id} data-testid={`release-experiment-option-${e.exp_id}`}>
								<ExperimentOption experiment={e} />
							</SelectItem>
						))}
					</SelectContent>
				</Select>
			</Field>

			{experiments.length === 0 && (
				<div className="flex flex-col gap-2 rounded-md border px-3 py-2 text-sm" data-testid="release-no-comparison">
					<p>{m("releaseForm.noComparison")}</p>
					<Button size="sm" variant="outline" className="w-fit" asChild>
						<Link to="/evaluate" search={{ mode: "versions" } as never} data-testid="release-evaluate-versions">
							{m("promote.evaluateNew")}
						</Link>
					</Button>
				</div>
			)}
			{promotable.length > 0 && (
				// M7-74: an evaluation is released from its own page; list the newest finished ones here.
				<div className="flex flex-col gap-1 text-xs" data-testid="release-evaluations">
					<span className="text-muted-foreground">{m("releaseForm.fromEvaluation")}</span>
					<ul className="flex flex-col gap-1">
						{promotable.map((ev) => (
							<li key={ev.id}>
								<Link to="/evaluations/$evalId" params={{ evalId: ev.id }} className="text-primary hover:underline">
									{ev.spec?.name || ev.id}
								</Link>
							</li>
						))}
					</ul>
				</div>
			)}

			<VerdictSummary {...props} />

			{verdict && blockers.length > 0 && (
				<div
					className="border-destructive/40 bg-destructive/5 flex flex-col gap-2 rounded-sm border px-3 py-2 text-sm"
					data-testid="release-blocked"
				>
					{blockers.map((b) => (
						<p key={b} className="flex items-start gap-2" data-testid={`release-blocker-${b}`}>
							<AlertTriangle className="text-chart-error-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
							{b === "notShip" ? t("releases.panel.blocked.notShip", { action: verdict.action }) : t("releases.panel.blocked.policyBlock")}
						</p>
					))}
					<label className="flex cursor-pointer items-center gap-2">
						<input
							type="checkbox"
							className="accent-primary size-4 cursor-pointer"
							checked={values.override}
							onChange={(e) => set({ override: e.target.checked })}
							data-testid="release-override-checkbox"
						/>
						<span className="font-medium">{t("releases.panel.override")}</span>
					</label>
					{values.override && (
						<Input
							aria-label={t("releases.panel.overrideReason")}
							placeholder={t("releases.panel.overridePlaceholder")}
							value={values.overrideReason}
							onChange={(e) => set({ overrideReason: e.target.value })}
							data-testid="release-override-reason"
						/>
					)}
					<p className="text-muted-foreground text-xs">{t("releases.panel.overrideHint")}</p>
				</div>
			)}

			<Field label={t("releases.panel.approver")} htmlFor="release-approver">
				<Input
					id="release-approver"
					placeholder={t("releases.panel.approverPlaceholder")}
					value={values.approver}
					onChange={(e) => set({ approver: e.target.value })}
					data-testid="release-approver-input"
				/>
			</Field>
			<Field label={t("releases.panel.note")} htmlFor="release-note">
				<Input
					id="release-note"
					placeholder={t("releases.panel.notePlaceholder")}
					value={values.note}
					onChange={(e) => set({ note: e.target.value })}
					data-testid="release-note-input"
				/>
			</Field>
			{/* Local mode only: hosted writes ride on the session cookie and its CSRF header. */}
			{usesWriteToken(session) && (
				<Field label={t("releases.panel.token")} htmlFor="release-token" hint={t("releases.panel.tokenHint")}>
					<Input
						id="release-token"
						type="password"
						autoComplete="off"
						placeholder={t("releases.panel.tokenPlaceholder")}
						value={token}
						onChange={(e) => {
							setToken(e.target.value);
							saveWriteToken(e.target.value);
						}}
						data-testid="release-token-input"
					/>
				</Field>
			)}

			<Button type="submit" disabled={!enabled} isLoading={props.submitting} dataTestId="release-submit">
				{t("releases.panel.submit")}
			</Button>
			{!enabled && disabledReason && (
				<p className="text-muted-foreground text-xs" data-testid="release-disabled-reason">
					{disabledReason.key === "error"
						? tu("releases.disabled.error", { error: disabledReason.error })
						: tu(`releases.disabled.${disabledReason.key}`)}
				</p>
			)}
			{props.submitError && (
				<p role="alert" className="text-chart-error-ink text-sm break-words" data-testid="release-submit-error">
					{t("releases.panel.error", { error: props.submitError })}
				</p>
			)}
			{props.created && (
				<p className="text-sm" data-testid="release-created">
					{t("releases.panel.created", { id: props.created.release_id })}
				</p>
			)}
		</form>
	);
}

function VerdictSummary({ verdict, policy, verdictLoading, verdictError, expId }: ReleaseFormProps) {
	const { t } = useTranslation("bench");
	if (!expId) return null;
	if (verdictLoading) {
		return (
			<p className="text-muted-foreground text-sm" data-testid="release-verdict-loading">
				{t("releases.panel.verdictLoading")}
			</p>
		);
	}
	if (verdictError || !verdict) {
		return (
			<p role="alert" className="text-chart-error-ink text-sm break-words" data-testid="release-verdict-error">
				{t("releases.panel.verdictError", { error: verdictError ?? "-" })}
			</p>
		);
	}
	return (
		<dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm" data-testid="release-verdict">
			<dt className="text-muted-foreground text-xs leading-6">{t("releases.panel.analysis")}</dt>
			<dd className="font-mono text-xs leading-6" data-testid="release-verdict-analysis">
				{verdict.analysisId}
			</dd>
			<dt className="text-muted-foreground text-xs leading-6">{t("releases.panel.action")}</dt>
			<dd className="flex gap-1.5">
				<Badge variant="outline">{verdict.decision}</Badge>
				<Badge variant={verdict.action === "ship" ? "success" : "warning"} data-testid="release-verdict-action">
					{verdict.action}
				</Badge>
			</dd>
			<dt className="text-muted-foreground text-xs leading-6">{t("releases.panel.policy")}</dt>
			<dd>
				<Badge
					variant={policy === "block" ? "destructive" : policy === "warn" ? "warning" : "outline"}
					className={cn(!policy && "text-muted-foreground")}
					data-testid="release-verdict-policy"
				>
					{policy ?? t("releases.panel.noPolicy")}
				</Badge>
			</dd>
		</dl>
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