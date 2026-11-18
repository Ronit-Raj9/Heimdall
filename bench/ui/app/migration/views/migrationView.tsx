import { useExperimentId } from "@/components/experiments/useExperimentId";
import { MigrationReportView } from "@/components/migration/migrationReport";
import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { useGetMigrationQuery } from "@/lib/store/apis/migrationApi";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";

/**
 * Model migration: /migration?exp=&from=&to=&condition=. The report of one model matrix experiment
 * (docs/model-migration.md); without exp it shows the newest experiment, without from arm A.
 */
export default function MigrationView() {
	const { t } = useTranslation("bench");
	const [expParam] = useQueryState("exp", parseAsString);
	const experiment = useExperimentId(expParam);
	return (
		<div className="flex flex-col gap-6" data-testid="migration-screen">
			<header className="flex flex-col gap-1">
				<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("migration.title")}</h1>
				<p className="text-muted-foreground max-w-prose text-sm">{t("migration.subtitle")}</p>
			</header>
			{experiment.isLoading ? (
				<BlockSkeleton rows={6} testId="migration-loading" />
			) : experiment.error ? (
				<LoadError title={t("migration.listError")} error={experiment.error} onRetry={experiment.refetch} testId="migration-list-error" />
			) : !experiment.expId ? (
				<EmptyNote testId="migration-no-experiments">{t("migration.noExperiments")}</EmptyNote>
			) : (
				<ExperimentMigration expId={experiment.expId} />
			)}
		</div>
	);
}

function ExperimentMigration({ expId }: { expId: string }) {
	const { t } = useTranslation("bench");
	const [from, setFrom] = useQueryState("from", parseAsString);
	const [to, setTo] = useQueryState("to", parseAsString);
	const [condition] = useQueryState("condition", parseAsString);
	const report = useGetMigrationQuery({ expId, from: from ?? undefined, to: to ?? undefined, condition: condition ?? undefined });
	return (
		<div className="flex flex-col gap-4">
			<div className="flex flex-wrap items-end gap-3 text-sm">
				<span className="text-muted-foreground font-mono text-xs">{t("migration.experiment", { id: expId })}</span>
				{report.data && <ModeBadge mode={report.data.mode} />}
				<label className="flex flex-col gap-1">
					<span className="text-muted-foreground text-xs">{t("migration.fromLabel")}</span>
					<input
						className="bg-background rounded-md border px-2 py-1 font-mono text-xs"
						defaultValue={from ?? ""}
						placeholder="A"
						data-testid="migration-from"
						onBlur={(e) => void setFrom(e.currentTarget.value.trim() || null)}
					/>
				</label>
				<label className="flex flex-col gap-1">
					<span className="text-muted-foreground text-xs">{t("migration.toLabel")}</span>
					<input
						className="bg-background rounded-md border px-2 py-1 font-mono text-xs"
						defaultValue={to ?? ""}
						placeholder="B,C"
						data-testid="migration-to"
						onBlur={(e) => void setTo(e.currentTarget.value.trim() || null)}
					/>
				</label>
			</div>
			{report.isLoading ? (
				<BlockSkeleton rows={6} testId="migration-report-loading" />
			) : report.error || !report.data ? (
				<LoadError title={t("migration.loadError")} error={report.error} onRetry={() => void report.refetch()} testId="migration-error" />
			) : (
				<MigrationReportView report={report.data} />
			)}
		</div>
	);
}