import { CanaryDetailBody } from "@/components/canaries/canaryDetail";
import { CanaryList } from "@/components/canaries/canaryList";
import { sortCanaries } from "@/components/canaries/canary.utils";
import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { useGetCanariesQuery, useGetCanaryQuery, useRunCanaryMutation } from "@/lib/store/apis/canaryApi";
import type { CanaryListResponse } from "@/lib/types/bench";
import { parseAsString, useQueryState } from "nuqs";
import type { ReactNode } from "react";
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
	const open = name ?? (data ? (sortCanaries(data.canaries)[0]?.name ?? null) : null);

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="canaries-screen">
			<header className="flex flex-col gap-1">
				<div className="flex flex-wrap items-center gap-3">
					<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("canaries.title")}</h1>
					<ModeBadge mode={data?.mode} isLoading={list.isLoading} isError={list.isError && !data} />
				</div>
				<p className="text-muted-foreground max-w-3xl text-sm">{t("canaries.subtitle")}</p>
			</header>
			{list.isLoading ? (
				<BlockSkeleton rows={4} testId="canaries-loading" />
			) : list.error || !data ? (
				<LoadError title={t("canaries.loadError")} error={list.error} onRetry={() => void list.refetch()} testId="canaries-error" />
			) : (
				<CanariesBody
					data={data}
					selected={open}
					onSelect={(next) => void setName(next)}
					panel={open ? <CanaryPanel key={open} name={open} /> : null}
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
}

/** The loaded list, definition errors, and the open canary. Exported for tests. */
export function CanariesBody({ data, selected, onSelect, panel }: CanariesBodyProps) {
	const { t } = useTranslation("bench");
	if (data.canaries.length === 0 && data.errors.length === 0) {
		return <EmptyNote testId="canaries-empty">{t("canaries.empty")}</EmptyNote>;
	}
	return (
		<div className="flex min-w-0 flex-col gap-6">
			<Section title={t("canaries.list.title")} description={t("canaries.list.description")} testId="canaries-list">
				<CanaryList canaries={data.canaries} selected={selected} onSelect={onSelect} />
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
function CanaryPanel({ name }: { name: string }) {
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
	return <CanaryDetailBody detail={detail.data} actions={<RunButton name={name} />} />;
}

/** POST /canaries/{name}/run with the write token; the look runs in the background. */
function RunButton({ name }: { name: string }) {
	const { t } = useTranslation("bench");
	const [run, state] = useRunCanaryMutation();
	return (
		<span className="flex items-center gap-2">
			<Button size="sm" variant="outline" disabled={state.isLoading} onClick={() => void run(name)} data-testid="canary-run">
				{t("canaries.run")}
			</Button>
			{state.isSuccess && <span className="text-muted-foreground text-xs">{t("canaries.runStarted")}</span>}
			{state.isError && (
				<span className="text-chart-error-ink text-xs" data-testid="canary-run-error">
					{t("canaries.runError")}
				</span>
			)}
		</span>
	);
}