import { GradingBadge, JudgedNotice, TrackBadge } from "@/components/scenario/scenarioBadges";
import { importCommand } from "@/components/scenario/scenario.utils";
import { EmptyNote } from "@/components/runs/screenStates";
import { CopyCodeBlock } from "@/components/suites/copyCodeBlock";
import type { ImporterInfo } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";

/**
 * One importer: the benchmarks it converts, the track and grading the suite gets, the upstream
 * license, how to fetch the data, what a run needs, and the exact commands to import, lock and
 * admit. Every value is text from the server; none is made a link.
 */
export function ImporterCard({ importer }: { importer: ImporterInfo }) {
	const { t } = useTranslation("bench");
	const varies = importer.license.trim().toLowerCase() === "varies";
	const rows: { key: string; label: string; value: React.ReactNode }[] = [
		{
			key: "benchmarks",
			label: t("imports.benchmarks"),
			value:
				importer.benchmarks.length > 0 ? (
					<ul className="flex flex-wrap gap-1">
						{importer.benchmarks.map((benchmark) => (
							<li key={benchmark} className="bg-muted rounded-sm px-1.5 py-0.5 font-mono text-[11px]">
								{benchmark}
							</li>
						))}
					</ul>
				) : (
					<span className="text-muted-foreground">{t("suites.facts.none")}</span>
				),
		},
		{
			key: "license",
			label: t("imports.license"),
			value: importer.license ? (
				<span>
					<code>{importer.license}</code>
					{varies && <span className="text-muted-foreground ml-2">{t("imports.licenseVaries")}</span>}
				</span>
			) : (
				<span className="text-chart-warning-ink">{t("suites.licenses.missing")}</span>
			),
		},
		{
			key: "fetch",
			label: t("imports.fetch"),
			value: importer.source ? (
				<code className="break-all">{importer.source}</code>
			) : (
				<span className="text-muted-foreground">{t("suites.facts.none")}</span>
			),
		},
		{
			key: "needs",
			label: t("imports.needs"),
			value:
				importer.needs.length > 0 ? (
					<ul className="flex list-disc flex-col gap-0.5 pl-4">
						{importer.needs.map((need) => (
							<li key={need}>{need}</li>
						))}
					</ul>
				) : (
					<span className="text-muted-foreground">{t("imports.needsNone")}</span>
				),
		},
	];
	return (
		<article className="bg-card flex h-full min-w-0 flex-col gap-3 rounded-sm border p-3" data-testid={`imports-card-${importer.name}`}>
			<div className="flex min-w-0 flex-col gap-1">
				<h2 className="text-foreground font-mono text-sm font-semibold">{importer.name}</h2>
				{importer.summary && <p className="text-muted-foreground text-xs">{importer.summary}</p>}
				<span className="flex flex-wrap items-center gap-1.5">
					<TrackBadge track={importer.track} testId={`imports-card-${importer.name}-track`} />
					<GradingBadge grading={importer.grading === "judged" ? "judged" : "verified"} testId={`imports-card-${importer.name}-grading`} />
				</span>
			</div>
			<dl className="grid min-w-0 grid-cols-1 gap-y-2 text-xs">
				{rows.map((row) => (
					<div key={row.key} className="flex min-w-0 flex-col gap-0.5" data-testid={`imports-card-${importer.name}-${row.key}`}>
						<dt className="text-muted-foreground">{row.label}</dt>
						<dd className="text-foreground min-w-0">{row.value}</dd>
					</div>
				))}
			</dl>
			{importer.grading === "judged" && <JudgedNotice testId={`imports-card-${importer.name}-judged`} />}
			<div className="mt-auto flex min-w-0 flex-col gap-1">
				<span className="text-muted-foreground text-xs">{t("imports.command")}</span>
				<CopyCodeBlock
					code={importCommand(importer.name)}
					label={t("imports.commandLabel", { name: importer.name })}
					testId={`imports-card-${importer.name}-command`}
				/>
			</div>
		</article>
	);
}

/** The importer cards, or a note that the server links none. */
export function ImporterList({ importers, fetching = false }: { importers: readonly ImporterInfo[]; fetching?: boolean }) {
	const { t } = useTranslation("bench");
	if (importers.length === 0) return <EmptyNote testId="imports-empty">{t("imports.empty")}</EmptyNote>;
	return (
		<ul
			className={cn("grid min-w-0 grid-cols-1 gap-3 lg:grid-cols-2 2xl:grid-cols-3", fetching && "opacity-60")}
			aria-label={t("imports.label")}
			data-testid="imports-list"
		>
			{importers.map((importer) => (
				<li key={importer.name} className="min-w-0">
					<ImporterCard importer={importer} />
				</li>
			))}
		</ul>
	);
}