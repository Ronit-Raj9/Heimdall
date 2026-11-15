import { getLocale } from "@/lib/i18n";
import type { ArtifactSpec } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";
import { ToneBadge } from "./suiteBadges";
import { formatBytes } from "./suites.utils";

export interface DeclaredArtifactsProps {
	artifacts: readonly ArtifactSpec[];
	/** The list is the suite's, used by a task that declares none of its own. */
	fromSuite?: boolean;
	/** Test id of the list; each item is `<testId>-<artifact id>`. */
	testId?: string;
}

/**
 * The files an artifact task (or suite) declares: id, workspace path (a glob is shown as written),
 * whether it is required, and the per-file size cap. Shown in the output facts; nothing here is a
 * link.
 */
export function DeclaredArtifacts({ artifacts, fromSuite = false, testId = "suites-task-artifacts" }: DeclaredArtifactsProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	if (artifacts.length === 0) return null;
	return (
		<div className="flex min-w-0 flex-col gap-1" data-testid={testId} data-from-suite={fromSuite}>
			<h5 className="text-foreground flex flex-wrap items-baseline gap-x-2 text-xs font-medium">
				{t("suites.scenario.artifacts")}
				{fromSuite && <span className="text-muted-foreground font-normal">{t("suites.scenario.fromSuite")}</span>}
			</h5>
			<p className="text-muted-foreground text-xs">{t("suites.scenario.artifactsHint")}</p>
			<ul className="flex min-w-0 flex-col gap-1" aria-label={t("suites.scenario.artifacts")}>
				{artifacts.map((artifact) => (
					<li
						key={artifact.id}
						className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 rounded-sm border px-2 py-1 text-xs"
						data-testid={`${testId}-${artifact.id}`}
						data-required={Boolean(artifact.required)}
					>
						<span className="text-foreground font-mono font-medium">{artifact.id}</span>
						<span className="text-muted-foreground font-mono break-all">{artifact.path}</span>
						<ToneBadge tone={artifact.required ? "warn" : "neutral"}>
							{artifact.required ? t("suites.scenario.artifactRequired") : t("suites.scenario.artifactOptional")}
						</ToneBadge>
						<span className="text-muted-foreground tabular-nums">
							{artifact.max_bytes
								? t("suites.scenario.artifactMaxBytes", { size: formatBytes(artifact.max_bytes, locale) })
								: t("suites.scenario.artifactDefaultMax")}
						</span>
					</li>
				))}
			</ul>
		</div>
	);
}