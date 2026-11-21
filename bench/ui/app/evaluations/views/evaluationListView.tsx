import { PageHeader } from "@/components/shell/pageHeader";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useListEvaluationsQuery, type Evaluation } from "@/lib/store/apis/evaluationsApi";
import { Link } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { useTranslation } from "react-i18next";
import { EVALUATION_POLL_MS, benchmarkCount, evaluationAgents, formatCreated, isActiveStatus, sortNewestFirst } from "./evaluation.utils";
import { EvaluationStatusPill, ModeLabel } from "./evaluationWords";

/** /evaluations: every evaluation, newest first; each row opens its progress and report card. */
export default function EvaluationListView() {
	const { t } = useTranslation("evaluations");
	const list = useListEvaluationsQuery();
	const anyActive = (list.data ?? []).some((e) => isActiveStatus(e.status));
	// A second subscription with polling only while something is still running.
	useListEvaluationsQuery(undefined, { pollingInterval: anyActive ? EVALUATION_POLL_MS : 0, skip: !anyActive });
	return (
		<div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6" data-testid="evaluations-screen">
			<PageHeader
				title={t("list.title")}
				subtitle={t("list.subtitle")}
				actions={
					<Button asChild size="sm">
						<Link to={"/evaluate" as never} data-testid="evaluations-new">
							<Plus className="size-4" aria-hidden="true" />
							{t("list.new")}
						</Link>
					</Button>
				}
			/>
			{list.isLoading ? (
				<BlockSkeleton rows={4} testId="evaluations-loading" />
			) : list.error || !list.data ? (
				<LoadError title={t("list.loadError")} error={list.error} onRetry={() => void list.refetch()} testId="evaluations-error" />
			) : (
				<EvaluationTable evaluations={list.data} />
			)}
		</div>
	);
}

/** The loaded list. Exported for tests. */
export function EvaluationTable({ evaluations }: { evaluations: readonly Evaluation[] }) {
	const { t, i18n } = useTranslation("evaluations");
	if (evaluations.length === 0) return <EmptyNote testId="evaluations-empty">{t("list.empty")}</EmptyNote>;
	return (
		<Table data-testid="evaluations-table">
			<TableHeader>
				<TableRow>
					<TableHead>{t("list.col.name")}</TableHead>
					<TableHead>{t("list.col.agents")}</TableHead>
					<TableHead>{t("list.col.mode")}</TableHead>
					<TableHead>{t("list.col.benchmarks")}</TableHead>
					<TableHead>{t("list.col.status")}</TableHead>
					<TableHead>{t("list.col.created")}</TableHead>
					<TableHead className="sr-only">{t("list.col.open")}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{sortNewestFirst(evaluations).map((ev) => (
					<TableRow key={ev.id} data-testid={`evaluation-row-${ev.id}`}>
						<TableCell className="font-medium">
							<Link to="/evaluations/$evalId" params={{ evalId: ev.id }} className="hover:underline">
								{ev.spec.name || ev.id}
							</Link>
						</TableCell>
						<TableCell>{evaluationAgents(ev).join(", ")}</TableCell>
						<TableCell>
							<ModeLabel mode={ev.spec.mode} />
						</TableCell>
						<TableCell className="tabular-nums">{t("list.benchmarkCount", { count: benchmarkCount(ev) })}</TableCell>
						<TableCell>
							<EvaluationStatusPill status={ev.status} testId={`evaluation-status-${ev.id}`} />
						</TableCell>
						<TableCell className="text-muted-foreground tabular-nums">{formatCreated(ev.created_at, i18n.language)}</TableCell>
						<TableCell className="text-right">
							<Link
								to="/evaluations/$evalId"
								params={{ evalId: ev.id }}
								className="text-primary text-sm hover:underline"
								data-testid={`evaluation-open-${ev.id}`}
							>
								{t("list.openOne")}
							</Link>
						</TableCell>
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}