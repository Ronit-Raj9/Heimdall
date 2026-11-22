import { DraftPreview, PublishPanel, ReadinessPanel } from "@/components/onboard/draftPanel";
import { InventoryPanel, MatchChips } from "@/components/onboard/inventoryPanel";
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
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

/** How often a draft is read again while its readiness checks or publish run. */
export const DRAFT_POLL_MS = 3000;

/**
 * /suites/add: drop anything, see what was found, preview each task with its checks and
 * controls, run the readiness checks live, publish privately.
 *
 * TODO(INT4): when lane y-13 (row mapper) is merged, map rows of a dataset input through its
 * row mapper here instead of the detect/inspect suggestion alone.
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
	const [setId, setSetId] = useState<string | null>(null);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");

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
		} catch (e) {
			setError(getBenchErrorMessage(e));
		} finally {
			setBusy(false);
		}
	};

	const onSource = async (c: SourceChoice) => {
		setSetId(null);
		setInv(null);
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
		await look(inp);
	};

	const onDraft = async () => {
		if (!input || !plan) return;
		setError("");
		try {
			const set = await createDraft({ input, plan }).unwrap();
			setSetId(set.id);
		} catch (e) {
			setError(getBenchErrorMessage(e));
		}
	};

	const reset = () => {
		setInput(null);
		setMatches([]);
		setInv(null);
		setPlan(null);
		setSetId(null);
		setError("");
	};

	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-8" data-testid="onboard-screen">
			<PageHeader
				title={t("title")}
				subtitle={t("subtitle")}
				breadcrumbs={[{ label: t("crumbs.benchmarks"), to: "/suites" }, { label: t("crumbs.add") }]}
			/>
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
					<InventoryPanel inv={inv} plan={plan} onPlan={setPlan} onDraft={() => void onDraft()} busy={draftState.isLoading} />
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
	const q = useGetOnboardDraftQuery(id, { pollingInterval: poll });
	const set = q.data;
	const active = !!set && (set.readiness?.status === "queued" || set.readiness?.status === "running" || (!!set.published && !set.published.job_id));
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

/** Earlier drafts, to pick up where one left off. */
function DraftList({ current, onOpen }: { current: string | null; onOpen(id: string): void }) {
	const { t } = useTranslation("onboard");
	const tx = (k: string, o?: Record<string, unknown>) => t(k as never, o as never) as unknown as string;
	const q = useListOnboardDraftsQuery();
	const [remove] = useDeleteOnboardDraftMutation();
	const drafts = q.data?.drafts ?? [];
	if (drafts.length === 0) return null;
	return (
		<Section title={t("drafts.title")} testId="onboard-drafts">
			<ul className="flex flex-col divide-y rounded-lg border text-sm">
				{drafts.map((d) => (
					<li key={d.id} className="flex flex-wrap items-center gap-2 px-3 py-2" data-testid={`onboard-draft-${d.id}`}>
						<span className="min-w-0 flex-1 truncate">
							{tx("drafts.row", { suite: d.suite, tasks: d.tasks, ready: d.ready })}
							<span className="text-muted-foreground ml-2 text-xs">{d.input}</span>
							{d.published && <span className="text-muted-foreground ml-2 text-xs">({t("drafts.published")})</span>}
						</span>
						{d.id !== current && (
							<Button size="sm" variant="outline" onClick={() => onOpen(d.id)}>
								{t("drafts.open")}
							</Button>
						)}
						<Button size="sm" variant="ghost" onClick={() => void remove(d.id)}>
							{t("drafts.delete")}
						</Button>
					</li>
				))}
			</ul>
		</Section>
	);
}
