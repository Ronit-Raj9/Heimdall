import { useConfirm } from "@/components/platform/confirmDialog";
import { CanaryDetailBody } from "@/components/canaries/canaryDetail";
import { CanaryList } from "@/components/canaries/canaryList";
import { sortCanaries } from "@/components/canaries/canary.utils";
import { PageHeader } from "@/components/shell";
import { BlockSkeleton, LoadError, Section } from "@/components/runs/screenStates";
import { FeatureHero } from "@/components/inbox/featureHero";
import { Bird, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { JobRunButton } from "@/components/jobs/jobRunButton";
import { useGetCanariesQuery, useGetCanaryQuery } from "@/lib/store/apis/canaryApi";
import {
	errorMessage,
	isPaused,
	useDeleteCanaryMutation,
	useGetSchedulesQuery,
	useSetCanaryPausedMutation,
} from "@/lib/store/apis/schedulesApi";
import { Badge } from "@/components/ui/badge";
import { NewCanaryForm } from "./newCanaryForm";
import type { CanaryDefinition, CanaryListResponse } from "@/lib/types/bench";
import { parseAsString, useQueryState } from "nuqs";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

/**
 * Drift canaries: /canaries?name=. A small pinned suite re-run on a schedule through the gateway
 * and tested against a known-good baseline (docs/drift-canary.md). The list shows every canary's
 * state; name opens one with its history chart, what changed and its looks.
 */
export default function CanariesView() {
	const { t } = useTranslation("bench");
	const [name, setName] = useQueryState("name", parseAsString);
	const list = useGetCanariesQuery();
	const data = list.data;
	// A name that is neither a monitor nor a broken definition is never opened: the page says so instead
	// (not while the list refreshes, so a monitor created a moment ago is not called unknown).
	const unknown = !list.isFetching && unknownCanary(data, name);
	const open = unknown ? null : (name ?? (data ? (sortCanaries(data.canaries)[0]?.name ?? null) : null));
	const { t: u } = useTranslation("ux13");
	const [formOpen, setFormOpen] = useState(false);
	const [editing, setEditing] = useState<CanaryDefinition | null>(null);
	const schedules = useGetSchedulesQuery();
	const paused = pausedCanaries(schedules.data?.rows);
	const hasAny = Boolean(data && (data.canaries.length > 0 || data.errors.length > 0));

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="canaries-screen">
			<PageHeader
				title={t("canaries.title")}
				subtitle={t("canaries.subtitle")}
				actions={
					hasAny && !formOpen ? (
						<Button size="sm" onClick={() => setFormOpen(true)} data-testid="canary-new-open">
							<Plus className="size-4" aria-hidden="true" />
							{u("canaries.newAction")}
						</Button>
					) : undefined
				}
			/>
			{formOpen && (
				<NewCanaryForm
					onClose={() => setFormOpen(false)}
					onCreated={(n) => {
						setFormOpen(false);
						void setName(n);
					}}
				/>
			)}
			{editing && (
				<NewCanaryForm key={editing.name} initial={editing} onClose={() => setEditing(null)} onCreated={() => setEditing(null)} />
			)}
			{unknown && name && <UnknownCanaryNotice name={name} onShowAll={() => void setName(null)} />}
			{list.isLoading ? (
				<BlockSkeleton rows={4} testId="canaries-loading" />
			) : list.error || !data ? (
				<LoadError title={t("canaries.loadError")} error={list.error} onRetry={() => void list.refetch()} testId="canaries-error" />
			) : (
				<CanariesBody
					data={data}
					selected={open}
					onSelect={(next) => void setName(next)}
					paused={paused}
					panel={
						open ? (
							<CanaryPanel
								key={open}
								name={open}
								onEdit={(d) => {
									setFormOpen(false);
									setEditing(d);
								}}
								onDeleted={() => void setName(null)}
							/>
						) : null
					}
					onCreate={formOpen ? undefined : () => setFormOpen(true)}
				/>
			)}
		</section>
	);
}

export interface CanariesBodyProps {
	data: CanaryListResponse;
	selected: string | null;
	onSelect: (name: string) => void;
	/** The open canary's panel; null when none is open. */
	panel: ReactNode;
	/** Opens the new-canary form; the empty state's primary action. */
	onCreate?: () => void;
	/** Names of the paused drift monitors. */
	paused?: ReadonlySet<string>;
}

/** Whether name is set and is neither a drift monitor nor a definition that failed to load. Exported for tests. */
export function unknownCanary(data: CanaryListResponse | undefined, name: string | null | undefined): boolean {
	if (!data || !name) return false;
	return !data.canaries.some((c) => c.name === name) && !data.errors.some((e) => e.name === name);
}

/** The notice for a link to a drift monitor that does not exist, with a way back to the list. Exported for tests. */
export function UnknownCanaryNotice({ name, onShowAll }: { name: string; onShowAll(): void }) {
	const { t } = useTranslation("schedules");
	return (
		<div
			role="status"
			className="border-border bg-muted/40 flex flex-wrap items-center gap-3 rounded-md border px-3 py-2 text-sm"
			data-testid="canary-unknown"
		>
			<span>{t("canary.unknown", { name })}</span>
			<Button size="sm" variant="outline" onClick={onShowAll} data-testid="canary-unknown-show-all">
				{t("canary.showAll")}
			</Button>
		</div>
	);
}

/** The drift monitors whose schedule is paused, from the schedules rows (canary-<name>). Exported for tests. */
export function pausedCanaries(rows: readonly { name: string; kind: string }[] | undefined): Set<string> {
	const out = new Set<string>();
	for (const r of rows ?? []) {
		if (r.kind === "canary" && r.name.startsWith("canary-") && isPaused(r)) out.add(r.name.slice("canary-".length));
	}
	return out;
}

/** The loaded list, definition errors, and the open canary. Exported for tests. */
export function CanariesBody({ data, selected, onSelect, panel, onCreate, paused }: CanariesBodyProps) {
	const { t } = useTranslation("bench");
	const { t: u } = useTranslation("ux13");
	if (data.canaries.length === 0 && data.errors.length === 0) {
		return (
			<FeatureHero
				icon={Bird}
				title={u("canaries.heroTitle")}
				message={u("canaries.heroMessage")}
				testId="canaries-empty"
				action={
					onCreate && (
						<Button onClick={onCreate} data-testid="canaries-hero-create">
							{u("canaries.heroAction")}
						</Button>
					)
				}
			>
				<span>{u("canaries.cli")}</span>
			</FeatureHero>
		);
	}
	return (
		<div className="flex min-w-0 flex-col gap-6">
			<Section title={t("canaries.list.title")} description={t("canaries.list.description")} testId="canaries-list">
				<CanaryList canaries={data.canaries} selected={selected} onSelect={onSelect} paused={paused} />
				{data.errors.length > 0 && (
					<ul className="text-chart-error-ink text-xs" data-testid="canaries-errors">
						{data.errors.map((e) => (
							<li key={e.name}>{t("canaries.definitionError", { name: e.name, error: e.error })}</li>
						))}
					</ul>
				)}
			</Section>
			{panel}
		</div>
	);
}

/** One canary, fetched, with the run button. */
function CanaryPanel({ name, onEdit, onDeleted }: { name: string; onEdit(d: CanaryDefinition): void; onDeleted(): void }) {
	const { t } = useTranslation("bench");
	const detail = useGetCanaryQuery(name);
	if (detail.isLoading) return <BlockSkeleton rows={6} testId="canary-loading" />;
	if (detail.error || !detail.data) {
		return (
			<LoadError
				title={t("canaries.detailError", { name })}
				error={detail.error}
				onRetry={() => void detail.refetch()}
				testId="canary-error"
			/>
		);
	}
	const def = detail.data.definition;
	return (
		<CanaryDetailBody
			detail={detail.data}
			actions={<CanaryActions name={name} definition={def} onEdit={() => onEdit(def)} onDeleted={onDeleted} />}
		/>
	);
}

/**
 * Baseline and look as background jobs (canary.baseline, canary.run) with a live log; pause or resume
 * the schedule, edit or delete the definition. Exported for tests.
 */
export function CanaryActions({
	name,
	definition,
	onEdit,
	onDeleted,
}: {
	name: string;
	definition?: CanaryDefinition;
	onEdit?(): void;
	onDeleted?(): void;
}) {
	const { t } = useTranslation("schedules");
	const confirm = useConfirm();
	const [remove, removeState] = useDeleteCanaryMutation();
	const [setPaused, pauseState] = useSetCanaryPausedMutation();
	const [note, setNote] = useState<string | null>(null);
	const scheduled = Boolean(definition?.schedule);
	const paused = isPaused(definition?.schedule);
	return (
		<span className="flex flex-wrap items-center gap-2" data-testid="canary-actions">
			{paused && (
				<Badge variant="outline" title={t("canary.pausedNote")} data-testid="canary-paused">
					{t("paused")}
				</Badge>
			)}
			<JobRunButton
				kind="canary.baseline"
				params={{ name }}
				label={t("canary.baseline")}
				confirm={t("canary.baselineConfirm")}
				variant="outline"
			/>
			<JobRunButton kind="canary.run" params={{ name }} label={t("canary.run")} variant="outline" />
			{!scheduled && (
				<span className="text-muted-foreground text-xs" data-testid="canary-no-schedule">
					{t("canary.noSchedulePause")}
				</span>
			)}
			{scheduled && (
				<Button
					size="sm"
					variant="outline"
					disabled={pauseState.isLoading}
					onClick={async () => {
						setNote(null);
						const res = await setPaused({ name, paused: !paused });
						if ("error" in res && res.error) setNote(errorMessage(res.error, "not changed"));
					}}
					data-testid={paused ? "canary-resume" : "canary-pause"}
				>
					{paused ? t("resume") : t("pause")}
				</Button>
			)}
			{onEdit && (
				<Button size="sm" variant="outline" onClick={onEdit} data-testid="canary-edit">
					{t("canary.edit")}
				</Button>
			)}
			<Button
				size="sm"
				variant="ghost"
				disabled={removeState.isLoading}
				onClick={async () => {
					if (!(await confirm(t("canary.deleteConfirm", { name })))) return;
					const res = await remove(name);
					if ("error" in res && res.error) setNote(errorMessage(res.error, "not deleted"));
					else onDeleted?.();
				}}
				data-testid="canary-delete"
			>
				{t("canary.delete")}
			</Button>
			{note && (
				<span className="text-xs" data-testid="canary-action-note">
					{note}
				</span>
			)}
		</span>
	);
}