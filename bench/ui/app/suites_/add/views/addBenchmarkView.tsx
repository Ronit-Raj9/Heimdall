import { DraftPreview, PublishPanel, ReadinessPanel } from "@/components/onboard/draftPanel";
import { InventoryPanel, MatchChips } from "@/components/onboard/inventoryPanel";
import { type DestructiveConfirmOptions, useConfirm } from "@/components/platform/confirmDialog";
import { OverflowMenu } from "@/components/agents/overflowMenu";
import { notify } from "@/lib/notify";
import { type SourceChoice, SourceBox } from "@/components/onboard/sourceBox";
import { BlockSkeleton, Section } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	useCreateOnboardDraftMutation,
	useDeleteOnboardDraftMutation,
	useDetectOnboardMutation,
	useGetOnboardDraftQuery,
	useInspectOnboardMutation,
	useListOnboardDraftsQuery,
	useUploadOnboardFileMutation,
} from "@/lib/store/apis/onboardApi";
import type { OnboardInput, OnboardInventory, OnboardMatch, OnboardPlan } from "@/lib/types/onboard";
import "@/lib/types/onboard";
import { AddChooser } from "@/components/suites/addChooserView";
import "@/components/suites/uf6bI18n";
import { usePageTitle } from "@/lib/routing/pageTitle";
import { cn } from "@/lib/utils";
import { useFormDraft } from "@/lib/hooks/useFormDraft";
import { useLeaveGuard } from "@/lib/hooks/useLeaveGuard";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	ONBOARD_STEPS,
	draftSourceName,
	keepableSource,
	stepReached,
	visibleDrafts,
} from "./addBenchmarkState";

/** How often a draft is read again while its readiness checks or publish run. */
/** Q-269: ask before leaving only when the user typed plan edits that no saved draft holds yet. */
export function leaveGuarded(s: { edited: boolean; drafted: boolean }): boolean {
	return s.edited && !s.drafted;
}

export const DRAFT_POLL_MS = 5000;

/** What a new source clears: the recognised chips, the found result and the draft (wave 4, p10-4). */
export function sourceReset(): { matches: OnboardMatch[]; inv: null; setId: null; error: string } {
	return { matches: [], inv: null, setId: null, error: "" };
}

/**
 * /suites/add: drop anything, see what was found, preview each task with its checks and
 * controls, run the readiness checks live, publish privately. Rows of a dataset input are mapped
 * with the Build tab's row mapper (MappingStep) into the draft plan's mapping.
 */
export default function AddBenchmarkView() {
	const { t } = useTranslation("onboard");
	const [upload] = useUploadOnboardFileMutation();
	const [detect] = useDetectOnboardMutation();
	const [inspect] = useInspectOnboardMutation();
	const [createDraft, draftState] = useCreateOnboardDraftMutation();
	const [input, setInput] = useState<OnboardInput | null>(null);
	const [matches, setMatches] = useState<OnboardMatch[]>([]);
	const [inv, setInv] = useState<OnboardInventory | null>(null);
	const [plan, setPlan] = useState<OnboardPlan | null>(null);
	// The draft id lives in the URL (?draft=), so a reload or a shared link reopens the draft (M6-52).
	const [setId, setDraftParam] = useQueryState("draft", parseAsString);
	const setSetId = (id: string | null) => void setDraftParam(id);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");

	const { t: tu } = useTranslation("uf6b");
	usePageTitle(tu("add.pageTitle"));

	// X-83: the looked-at link or pasted rows are a form draft (shared useFormDraft), so they survive
	// a reload and are looked at again; leaving with a source that has no saved draft asks first.
	const [kept, updateKept, clearKept] = useFormDraft<{ source: OnboardInput | null }>("addBenchmark.source", { source: null });
	const keep = (inp: OnboardInput | null) => {
		const k = keepableSource(inp);
		if (k) updateKept({ source: k });
		else clearKept();
	};
	const restored = useRef(false);
	useEffect(() => {
		if (restored.current) return;
		restored.current = true;
		if (setId) return;
		const again = keepableSource(kept.source);
		if (again) {
			setInput(again);
			void look(again);
		}
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, []);
	// Q-269: the source itself is kept as a draft and looked at again on return, so only plan edits
	// the user typed ask before leaving; the app's own next-step links clear the flag first.
	const [edited, setEdited] = useState(false);
	useLeaveGuard(leaveGuarded({ edited, drafted: !!setId }));

	const look = async (inp: OnboardInput, source?: string) => {
		setError("");
		setBusy(true);
		try {
			const det = await detect({ input: inp }).unwrap();
			setMatches(det.matches);
			if (det.matches.length === 0) {
				setInv(null);
				setError(det.next);
				return;
			}
			const found = await inspect({ input: inp, source: source ?? det.matches[0].source }).unwrap();
			setInv(found);
			setPlan(found.suggest);
			setEdited(false);
		} catch (e) {
			setMatches([]);
			setError(getBenchErrorMessage(e));
		} finally {
			setBusy(false);
		}
	};

	const onSource = async (c: SourceChoice) => {
		const reset = sourceReset();
		setMatches(reset.matches);
		setSetId(reset.setId);
		setInv(reset.inv);
		setError(reset.error);
		let inp = c.input;
		if (c.file) {
			setBusy(true);
			try {
				const up = await upload(c.file).unwrap();
				inp = { upload: up.upload, name: c.file.name, connection: c.input.connection };
			} catch (e) {
				setError(getBenchErrorMessage(e));
				setBusy(false);
				return;
			}
		}
		setInput(inp);
		keep(inp);
		await look(inp);
	};

	const onDraft = async () => {
		if (!input || !plan) return;
		setError("");
		try {
			const set = await createDraft({ input, plan }).unwrap();
			setSetId(set.id);
			setEdited(false);
			keep(null);
		} catch (e) {
			setError(getBenchErrorMessage(e));
		}
	};

	const reset = () => {
		setInput(null);
		setMatches([]);
		setInv(null);
		setPlan(null);
		setEdited(false);
		setSetId(null);
		setError("");
		keep(null);
	};

	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-8" data-testid="onboard-screen">
			<PageHeader
				title={t("title")}
				subtitle={t("subtitle")}
				breadcrumbs={[{ label: t("crumbs.benchmarks"), to: "/suites" }, { label: t("crumbs.add") }]}
			/>
			{!input && !setId && <AddChooser current="file" />}
			<StepLine reached={stepReached({ found: !!inv, draft: !!setId })} />
			<Section title={t("steps.source")} description={t("source.description")} testId="onboard-step-source">
				<SourceBox busy={busy} onSubmit={(c) => void onSource(c)} onClear={input ? reset : undefined} />
				<MatchChips matches={matches} active={inv?.source} onPick={(s) => input && void look(input, s)} />
				{error && (
					<p role="alert" className="text-chart-error-ink text-sm" data-testid="onboard-error">
						{error}
					</p>
				)}
			</Section>
			{inv && plan && !setId && (
				<Section title={t("steps.found")} testId="onboard-step-found">
					<InventoryPanel
						inv={inv}
						plan={plan}
						onPlan={(p) => {
							setPlan(p);
							setEdited(true);
						}}
						onLeave={() => setEdited(false)}
						onDraft={() => void onDraft()}
						busy={draftState.isLoading}
					/>
				</Section>
			)}
			{setId && <DraftSteps id={setId} />}
			<DraftList current={setId} onOpen={(id) => setSetId(id)} />
		</section>
	);
}

/** Steps 3 to 5 over one saved draft, read again while work runs on it. */
export function DraftSteps({ id }: { id: string }) {
	const { t } = useTranslation("onboard");
	const [poll, setPoll] = useState(0);
	const q = useGetOnboardDraftQuery(id, { pollingInterval: poll, skipPollingIfUnfocused: true });
	const set = q.data;
	const active =
		!!set && (set.readiness?.status === "queued" || set.readiness?.status === "running" || (!!set.published && !set.published.job_id));
	useEffect(() => setPoll(active ? DRAFT_POLL_MS : 0), [active]);
	if (!set) {
		return q.isError ? (
			<p role="alert" className="text-chart-error-ink text-sm">
				{t("error")}: {getBenchErrorMessage(q.error)}
			</p>
		) : (
			<BlockSkeleton rows={4} testId="onboard-draft-loading" />
		);
	}
	return (
		<>
			<Section title={t("steps.preview")} description={t("preview.description")} testId="onboard-step-preview">
				<DraftPreview set={set} />
			</Section>
			<Section title={t("steps.ready")} description={t("ready.description")} testId="onboard-step-ready">
				<ReadinessPanel set={set} />
			</Section>
			<Section title={t("steps.publish")} description={t("publish.description")} testId="onboard-step-publish">
				<PublishPanel set={set} />
			</Section>
		</>
	);
}

/** The five steps as one line, future steps greyed, so steps 2 to 5 are known from the start (M6-51). */
export function StepLine({ reached }: { reached: number }) {
	const { t } = useTranslation("onboard");
	const { t: tu } = useTranslation("uf6b");
	return (
		<ol aria-label={tu("add.steps")} className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs" data-testid="onboard-step-line">
			{ONBOARD_STEPS.map((step, i) => (
				<li
					key={step}
					aria-current={i === reached ? "step" : undefined}
					data-state={i < reached ? "done" : i === reached ? "current" : "future"}
					className={cn(
						"whitespace-nowrap",
						i <= reached ? "text-foreground font-medium" : "text-muted-foreground",
					)}
				>
					{t(`steps.${step}`)}
				</li>
			))}
		</ol>
	);
}

/**
 * Earlier drafts, to pick up where one left off: collapsed with its count, each row the source's
 * file name and the date on two lines, dead empty drafts left out (M6-53).
 */
function DraftList({ current, onOpen }: { current: string | null; onOpen(id: string): void }) {
	const { t, i18n } = useTranslation("onboard");
	const { t: tu } = useTranslation("uf6b");
	const tx = (k: string, o?: Record<string, unknown>) => t(k as never, o as never) as unknown as string;
	const q = useListOnboardDraftsQuery();
	const [remove, removeState] = useDeleteOnboardDraftMutation();
	const confirm = useConfirm();
	// Delete sits in the row's More menu (M6-53) and asks first, naming the draft.
	const askDelete = async (d: { id: string; suite: string; tasks: number }) => {
		const opts: DestructiveConfirmOptions = {
			destructive: true,
			title: tu("add.draftDeleteTitle", { suite: d.suite }),
			confirmLabel: t("drafts.deleteYes"),
			cancelLabel: t("drafts.keep"),
		};
		if (!(await confirm(tx("drafts.deleteAsk", { suite: d.suite, tasks: d.tasks }), opts))) return;
		try {
			await remove(d.id).unwrap();
			notify({ text: tx("deleted", { ns: "toast", name: d.suite }) });
		} catch {
			// The error line above the list shows why (removeState.error).
		}
	};
	const drafts = visibleDrafts(q.data?.drafts ?? [], Date.now(), current);
	if (drafts.length === 0) return null;
	const day = (iso: string) => {
		const at = new Date(iso);
		return Number.isNaN(at.getTime()) ? "" : at.toLocaleDateString(i18n.language, { day: "numeric", month: "short", year: "numeric" });
	};
	return (
		<details className="group flex min-w-0 flex-col gap-3" data-testid="onboard-drafts">
			<summary className="cursor-pointer text-sm font-medium">{tu("add.drafts", { count: drafts.length })}</summary>
			{removeState.error !== undefined && (
				<p role="alert" className="text-chart-error-ink mt-2 text-sm" data-testid="onboard-draft-delete-error">
					{tx("drafts.deleteFailed", { message: getBenchErrorMessage(removeState.error) })}
				</p>
			)}
			<ul className="mt-2 flex flex-col divide-y rounded-lg border text-sm">
				{drafts.map((d) => (
					<li key={d.id} className="flex min-w-0 items-center gap-2 px-3 py-2" data-testid={`onboard-draft-${d.id}`}>
						<div className="flex min-w-0 flex-1 flex-col">
							<span className="truncate">
								{tx("drafts.row", { suite: d.suite, tasks: d.tasks, ready: d.ready })}
								{d.published && <span className="text-muted-foreground ml-2 text-xs">({t("drafts.published")})</span>}
							</span>
							<span className="text-muted-foreground truncate text-xs" title={d.input}>
								{[draftSourceName(d.input), day(d.updated)].filter(Boolean).join(" · ")}
							</span>
						</div>
						{d.id !== current && (
							<Button size="sm" variant="outline" onClick={() => onOpen(d.id)}>
								{t("drafts.open")}
							</Button>
						)}
						<OverflowMenu
							label={tu("add.draftMore", { suite: d.suite })}
							items={[{ label: t("drafts.delete"), onSelect: () => void askDelete(d), destructive: true, testId: `onboard-draft-delete-${d.id}` }]}
							testId={`onboard-draft-more-${d.id}`}
						/>
					</li>
				))}
			</ul>
		</details>
	);
}
