import { defaultJobModel, plainJobError } from "@/components/findings/jobModel";
import "@/components/findings/uff1I18n";
import { GroupedModelPicker, type PickedModel } from "@/components/models/groupedModelPicker";
import { Button } from "@/components/ui/button";
import { DropdownMenu } from "@/components/ui/dropdownMenu";
import { Input } from "@/components/ui/input";
import { PaidButton } from "@/components/ui/paidButton";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useAcceptInboxItemMutation } from "@/lib/store/apis/inboxApi";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import { formatUsd } from "@/lib/money";
import { useGetModelsQuery } from "@/lib/store/apis/modelsApi";
import { useGetJobQuery } from "@/lib/store/apis/platformApi";
import { useGetInboxProposalQuery, type ProposalReason, type TraceProposal } from "@/lib/store/apis/traceDraftApi";
import type { InboxAcceptRequest, InboxTaskKind } from "@/lib/types/bench";
import { ChevronDown, Sparkles } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useGetDraftEstimateQuery } from "./draftEstimateApi";
import { useTranslation } from "react-i18next";
import { guideFormErrors, guideFrom, isToolKind, type GuideCallRow, type GuideFormValues } from "./inboxGuide.utils";
import "./w445I18n";
import "@/app/inbox/views/uf7aI18n";
import "./ufk4I18n";

/**
 * Enter in a field of the paid draft panel does nothing: the panel sits inside the accept form, and Enter
 * there would submit that form (or start paid work) on one key press. Exported for tests.
 */
export function blockEnterSubmit(e: Pick<KeyboardEvent, "key" | "target" | "preventDefault">): void {
	if (e.key === "Enter" && (e.target as { tagName?: string } | null)?.tagName === "INPUT") e.preventDefault();
}

/** Default spending cap of one draft, in US dollars (the job caps it at 2.00). */
export const DEFAULT_DRAFT_BUDGET = 0.5;

/**
 * The trace.draft job as the server reads it (jobs_tracedraft_w445.go traceDraftParams: item, suite,
 * kind, provider, model, budget_usd, yes; unknown fields refused). Exported so a test can hold it
 * against the server's field list.
 */
export function traceDraftJob(item: string, suite: string, kind: InboxTaskKind, model: PickedModel | null, budget: number) {
	return { kind: "trace.draft", params: { item, suite, kind, provider: model?.provider ?? "", model: model?.model ?? "", budget_usd: budget, yes: true } };
}

/** The estimate line of the draft confirm, or null while it loads. */
export function draftEstimateText(
	u: (k: "draft.estimate" | "draft.estimateUnknown", o?: Record<string, unknown>) => string,
	e: { priced: boolean; usd: number; max_usd: number } | undefined,
): string | null {
	if (!e) return null;
	if (!e.priced) return u("draft.estimateUnknown");
	return u("draft.estimate", { usd: formatUsd(e.usd), max: formatUsd(e.max_usd) });
}

/** The form values a proposal fills in: the guide fields of the kind, every call the model named. */
export function prefillFromProposal(p: TraceProposal, kind: InboxTaskKind): Partial<GuideFormValues> {
	const g = p.guide;
	const patch: Partial<GuideFormValues> = { mode: "guided" };
	if (g.instruction) patch.instruction = g.instruction;
	if (isToolKind(kind)) {
		const row = (verdict: GuideCallRow["verdict"]) => (c: { tool: string; args?: Record<string, unknown> }) => ({
			tool: c.tool,
			args: c.args && Object.keys(c.args).length > 0 ? JSON.stringify(c.args) : "",
			verdict,
			added: true,
		});
		patch.calls = [...(g.calls ?? []).map(row("right")), ...(g.forbidden ?? []).map(row("wrong"))];
		patch.ordered = Boolean(g.ordered);
		patch.replyRubric = g.reply_rubric ?? "";
	} else {
		patch.answers = (g.answers ?? []).join("\n");
		patch.recordedWrong = Boolean(g.recorded_wrong);
	}
	return patch;
}

/** The guide fields a proposal filled in, mapped to the reasons the model gave for each. */
function suggestedFields(p: TraceProposal, kind: InboxTaskKind): { field: ProposalReason["field"]; reasons: ProposalReason[] }[] {
	const g = p.guide;
	const fields: ProposalReason["field"][] = [];
	if (g.instruction) fields.push("instruction");
	if (isToolKind(kind)) {
		if (g.calls?.length) fields.push("calls");
		if (g.forbidden?.length) fields.push("forbidden");
		if (g.reply_rubric) fields.push("reply_rubric");
	} else if (g.answers?.length) {
		fields.push("answers");
	}
	return fields.map((field) => ({ field, reasons: p.reasons.filter((r) => r.field === field) }));
}

/** What the review buttons call: the accept, then (when checking) the readiness job. */
export interface ReviewCalls {
	accept: (args: { id: string; body: InboxAcceptRequest }) => Promise<{ draft: { finished: boolean; task: string } }>;
	createJob: (body: { kind: string; params: object }) => Promise<{ id: string }>;
}

/**
 * "Approve and add to the benchmark" (check true) and "Save without checking" (check false): the
 * accept with the edited guide, then, when the accept wrote a finished task and check is set,
 * the readiness job suite.admit for it. Returns that job's id, if one was started.
 */
export async function submitReview({
	itemId,
	suite,
	kind,
	values,
	check,
	accept,
	createJob,
}: ReviewCalls & { itemId: string; suite: string; kind: InboxTaskKind; values: GuideFormValues; check: boolean }): Promise<{
	admitJob?: string;
}> {
	const res = await accept({ id: itemId, body: { suite, kind, guide: guideFrom(values, kind) } });
	if (!check || !res.draft.finished) return {};
	const task = res.draft.task.split("/").slice(1).join("/");
	const job = await createJob({ kind: "suite.admit", params: { suite, task } });
	return { admitJob: job.id };
}

export interface ProposalBarProps {
	itemId: string;
	suite: string;
	kind: InboxTaskKind;
	values: GuideFormValues;
	onChange: (patch: Partial<GuideFormValues>) => void;
	/** Scrolls the session panel to a turn (seq 0) or a call (seq n), when the page shows one. */
	onQuote?: (seq: number) => void;
}

/**
 * The top of the review screen (wave 4, trace to environment): "Draft with a model" asks for the
 * cost, runs one model call, and fills the form from the draft; each filled field says it was
 * suggested by a model, with the quotes it rests on. "Approve and add to the benchmark" accepts
 * the item with the edited guide and starts the readiness check; "Save without checking" accepts only.
 */
export function ProposalBar({ itemId, suite, kind, values, onChange, onQuote }: ProposalBarProps) {
	const { t } = useTranslation("w445");
	const { t: f } = useTranslation("uf7a");
	const { t: k4 } = useTranslation("ufk4");
	const [confirming, setConfirming] = useState(false);
	const barRef = useRef<HTMLDivElement>(null);
	const [budget, setBudget] = useState(String(DEFAULT_DRAFT_BUDGET));
	const [draftJob, setDraftJob] = useState<string | null>(null);
	const [admitJob, setAdmitJob] = useState<string | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [done, setDone] = useState<string | null>(null);
	const [prefilled, setPrefilled] = useState<string | null>(null);
	const [createJob, { isLoading: starting }] = useCreateJobMutation();
	const [accept, { isLoading: accepting }] = useAcceptInboxItemMutation();
	const proposal = useGetInboxProposalQuery(itemId);
	const draft = useGetJobQuery(draftJob ?? "", { skip: !draftJob, pollingInterval: draftJob ? 5000 : 0, skipPollingIfUnfocused: true });
	const admit = useGetJobQuery(admitJob ?? "", { skip: !admitJob, pollingInterval: admitJob ? 5000 : 0, skipPollingIfUnfocused: true });

	const draftStatus = draft.data?.status;
	const { refetch } = proposal;
	useEffect(() => {
		if (!draftJob || !draftStatus) return;
		if (draftStatus === "succeeded") {
			setDraftJob(null);
			void refetch();
		} else if (draftStatus === "failed" || draftStatus === "cancelled") {
			setDraftJob(null);
			setError(plainJobError(draft.data?.error ?? draftStatus));
		}
	}, [draftJob, draftStatus, draft.data?.error, refetch]);

	const p = proposal.data;
	useEffect(() => {
		if (p && p.created_at !== prefilled) {
			setPrefilled(p.created_at);
			onChange(prefillFromProposal(p, kind));
		}
	}, [p, prefilled, kind, onChange]);

	const admitStatus = admit.data?.status;
	useEffect(() => {
		if (!admitJob || !admitStatus) return;
		if (admitStatus === "succeeded") {
			setAdmitJob(null);
			setDone(t("proposal.added", { benchmark: suite }));
		} else if (admitStatus === "failed" || admitStatus === "cancelled") {
			setAdmitJob(null);
			setError(plainJobError(admit.data?.error ?? admitStatus));
		}
	}, [admitJob, admitStatus, admit.data?.error, suite, t]);

	const startDraft = async (chosen: PickedModel | null) => {
		setError(null);
		try {
			const job = await createJob(traceDraftJob(itemId, suite, kind, chosen, Number(budget))).unwrap();
			// K4: no job id is a start that did not happen; say so instead of showing nothing.
			if (!job?.id) {
				setError(k4("draft.noJob"));
				return;
			}
			setConfirming(false);
			setDraftJob(job.id);
		} catch (e) {
			setError(plainJobError(getBenchErrorMessage(e)));
		}
	};

	const submit = async (check: boolean) => {
		setError(null);
		setDone(null);
		try {
			const out = await submitReview({
				itemId,
				suite,
				kind,
				values,
				check,
				accept: (args) => accept(args).unwrap(),
				createJob: (body) => createJob(body).unwrap(),
			});
			if (out.admitJob) setAdmitJob(out.admitJob);
			else setDone(t("proposal.saved", { benchmark: suite }));
		} catch (e) {
			setError(getBenchErrorMessage(e));
		}
	};

	const busy = starting || accepting || Boolean(draftJob) || Boolean(admitJob);
	const invalid = values.mode !== "guided" || guideFormErrors(values, kind).length > 0;

	return (
		<div ref={barRef} className="border-border flex flex-col gap-2 rounded-sm border px-3 py-2" data-testid="proposal-bar">
			{/* One primary (add the task), the other ways in "More ways" (M7-4). */}
			<div className="flex flex-wrap items-center gap-2">
				<Button type="button" size="sm" disabled={busy || invalid} onClick={() => void submit(true)} dataTestId="proposal-approve">
					{f("accept.primary", { benchmark: suite })}
				</Button>
				<DropdownMenu
					label={f("accept.more")}
					align="start"
					testId="proposal-more"
					trigger={(props) => (
						<Button {...props} size="sm" variant="outline" disabled={busy} dataTestId="proposal-more-button">
							{f("accept.more")}
							<ChevronDown className="size-3.5" aria-hidden="true" />
						</Button>
					)}
					items={[
						{
							id: "draft",
							icon: Sparkles,
							label: f("accept.draft", { budget: Number(budget).toFixed(2) }),
							onSelect: () => setConfirming(true),
							testId: "proposal-draft",
						},
						{
							id: "save",
							label: t("proposal.saveOnly"),
							disabled: invalid,
							// Inside the accept form this is the form's own accept (task id and grader switch kept); alone it saves the guide.
							onSelect: () => {
								const form = barRef.current?.closest("form");
								if (form) form.requestSubmit();
								else void submit(false);
							},
							testId: "proposal-save",
						},
					]}
				/>
			</div>
			{confirming && (
				<DraftPanel
					itemId={itemId}
					kind={kind}
					budget={budget}
					setBudget={setBudget}
					starting={starting}
					onStart={startDraft}
					onCancel={() => setConfirming(false)}
				/>
			)}
			{confirming && error && (
				<p className="text-danger-ink text-xs" role="alert" data-testid="proposal-start-error">
					{error}
				</p>
			)}
			{draftJob && (
				<p className="text-muted-foreground text-xs" role="status" data-testid="proposal-drafting">
					{t("proposal.drafting")}
				</p>
			)}
			{p && (
				<ul className="flex flex-col gap-1.5" data-testid="proposal-suggestions">
					{suggestedFields(p, kind).map(({ field, reasons }) => (
						<li key={field} className="text-xs" data-testid={`proposal-field-${field}`}>
							<span className="bg-muted mr-2 rounded-sm px-1.5 py-0.5 font-medium">{t("proposal.suggested")}</span>
							{reasons.length === 0 ? (
								<span className="text-muted-foreground" data-testid={`proposal-noquote-${field}`}>
									{t("proposal.noQuote")}
								</span>
							) : (
								reasons.map((r, i) => (
									<span key={i} className="mr-2">
										{r.text}{" "}
										{r.quotes.map((q, j) => (
											<button
												key={j}
												type="button"
												className="text-primary mr-1 underline"
												onClick={() => onQuote?.(q.seq)}
												data-testid={`proposal-quote-${field}-${i}-${j}`}
											>
												&quot;{q.text}&quot;
											</button>
										))}
									</span>
								))
							)}
						</li>
					))}
					{p.dropped_unverified > 0 && (
						<li className="text-muted-foreground text-xs" data-testid="proposal-dropped">
							{t("proposal.dropped", { count: p.dropped_unverified })}
						</li>
					)}
				</ul>
			)}
			{admitJob && (
				<p className="text-xs" role="status" data-testid="proposal-proving">
					{t("proposal.proving")}
				</p>
			)}
			{done && (
				<p className="text-xs" role="status" data-testid="proposal-done">
					{done}
				</p>
			)}
			{error && !confirming && (
				<p className="text-danger-ink text-xs" role="alert" data-testid="proposal-error">
					{error}
				</p>
			)}
		</div>
	);
}
/**
 * The paid draft panel: the model picker opening on the org's default working model (PT-2), the
 * cap, and the estimate from the server's price list (PT-8). Mounted only while it is open, so
 * its model and estimate queries run only then.
 */
function DraftPanel({
	itemId,
	kind,
	budget,
	setBudget,
	starting,
	onStart,
	onCancel,
}: {
	itemId: string;
	kind: InboxTaskKind;
	budget: string;
	setBudget: (v: string) => void;
	starting: boolean;
	onStart: (m: PickedModel | null) => void | Promise<unknown>;
	onCancel: () => void;
}) {
	const { t } = useTranslation("w445");
	const { t: f } = useTranslation("uf7a");
	const { t: u } = useTranslation("uff1");
	const ids = useId();
	const models = useGetModelsQuery();
	const [picked, setPicked] = useState<PickedModel | null>(null);
	const fallback = useMemo(() => defaultJobModel(models.data), [models.data]);
	const chosen = picked ?? fallback;
	const provider = chosen?.provider ?? "";
	const model = chosen?.model ?? "";
	const est = useGetDraftEstimateQuery({ item: itemId, model, kind }, { skip: !model });
	const estimate = draftEstimateText(u, est.data);
	return (
				<div
					className="bg-muted flex flex-col gap-2 rounded-sm px-3 py-2"
					role="dialog"
					aria-label={t("proposal.draft")}
					data-testid="proposal-confirm"
					onKeyDown={blockEnterSubmit}
				>
					<p className="text-sm">{t("proposal.confirm", { budget: Number(budget).toFixed(2) })}</p>
					{estimate ? (
						<p className="text-muted-foreground text-xs" data-testid="proposal-estimate">
							{estimate}
						</p>
					) : null}
					<div className="flex flex-wrap items-end gap-2">
						<div className="flex min-w-56 flex-col gap-0.5 text-xs">
							<span>{u("model.label")}</span>
							<GroupedModelPicker value={chosen} onChange={setPicked} label={u("model.label")} testId="proposal-model" />
						</div>
						<label className="flex flex-col gap-0.5 text-xs" htmlFor={`${ids}-budget`}>
							{t("proposal.budget")}
							<Input
								id={`${ids}-budget`}
								className="h-8 w-24"
								type="number"
								min="0.01"
								max="2"
								step="0.01"
								value={budget}
								onChange={(e) => setBudget(e.target.value)}
								data-testid="proposal-budget"
							/>
						</label>
						{/* A paid model call: it always asks once more through the shared confirm, which opens on Cancel. */}
						<PaidButton
							size="sm"
							verb={f("accept.draftVerb")}
							capUsd={Number(budget) > 0 ? Number(budget) : null}
							confirm="always"
							kind="form"
							confirmText={u("draft.ask", { model, estimate: estimate ?? "", cap: Number(budget) > 0 ? Number(budget).toFixed(2) : "" })}
							disabled={starting || !provider || !model || !(Number(budget) > 0)}
							onConfirm={() => onStart(chosen)}
							testId="proposal-start"
						/>
						<Button type="button" size="sm" variant="ghost" onClick={onCancel} dataTestId="proposal-cancel">
							{t("proposal.cancel")}
						</Button>
					</div>
				</div>
	);
}
