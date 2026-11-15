import { formatBytes } from "@/components/suites/suites.utils";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { shortHash } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import { BENCH_API_BASE } from "@/lib/store/apis/fixtures";
import type { ArtifactFile } from "@/lib/types/bench";
import { Download } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * Where one artifact file downloads from: GET /runs/{id}/artifacts/{artifact}/{name}. Every segment
 * is encoded, so a name can never add a path segment or a query.
 */
export function artifactDownloadUrl(runId: string, file: Pick<ArtifactFile, "id" | "name">): string {
	return `${BENCH_API_BASE}/runs/${encodeURIComponent(runId)}/artifacts/${encodeURIComponent(file.id)}/${encodeURIComponent(file.name ?? "")}`;
}

/** Whether a file was copied into the run folder and can be offered for download. */
export function isExported(file: ArtifactFile): boolean {
	return !file.error && Boolean(file.name) && Boolean(file.path);
}

export interface ArtifactFilesProps {
	files: readonly ArtifactFile[];
	runId: string;
	arm: string;
	/** Files are served only from a sealed run. */
	sealed: boolean;
	/** Set when the sealed folder no longer matches its seal; the server then refuses every file. */
	sealError?: string;
}

/**
 * An artifact task's files: id, name, size and hash, with a download link for each exported file
 * and the export error in its place for one that was not copied. The files are agent-written: they
 * are only ever downloaded (the server sends them as attachments), never shown or rendered here.
 */
export function ArtifactFiles({ files, runId, arm, sealed, sealError }: ArtifactFilesProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	return (
		<section className="flex min-w-0 flex-col gap-2" data-testid={`run-artifacts-${arm}`}>
			<div className="flex flex-col gap-0.5">
				<h3 className="text-foreground text-xs font-semibold tracking-wide uppercase">{t("runs.scenario.artifacts.title")}</h3>
				<p className="text-muted-foreground max-w-prose text-xs">{t("runs.scenario.artifacts.description")}</p>
			</div>
			<div className="min-w-0 rounded-sm border">
				<Table aria-label={t("runs.scenario.artifacts.label", { arm })}>
					<TableHeader>
						<TableRow>
							<TableHead scope="col">{t("runs.scenario.artifacts.artifact")}</TableHead>
							<TableHead scope="col">{t("runs.scenario.artifacts.file")}</TableHead>
							<TableHead scope="col" className="text-right">
								{t("runs.scenario.artifacts.size")}
							</TableHead>
							<TableHead scope="col">{t("runs.scenario.artifacts.hash")}</TableHead>
							<TableHead scope="col">{t("runs.scenario.artifacts.download")}</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{files.map((file, i) => {
							const exported = isExported(file);
							return (
								<TableRow
									key={file.path || `${file.id}/${file.name ?? ""}/${i}`}
									data-testid={`run-artifact-${file.id}${file.name ? `-${file.name}` : ""}`}
									data-exported={exported}
								>
									<TableCell className="align-top font-mono text-xs">{file.id}</TableCell>
									<TableCell className="align-top text-xs whitespace-normal">
										<span className="flex min-w-0 flex-col gap-0.5">
											{file.name && <span className="text-foreground font-mono break-all">{file.name}</span>}
											{file.source && (
												<span className="text-muted-foreground font-mono text-[11px] break-all">
													{t("runs.scenario.artifacts.from", { source: file.source })}
												</span>
											)}
										</span>
									</TableCell>
									<TableCell className="text-right align-top text-xs tabular-nums">
										{exported ? formatBytes(file.bytes, locale) : ""}
									</TableCell>
									<TableCell className="align-top font-mono text-xs">
										{exported && file.sha256 ? <span title={file.sha256}>{shortHash(file.sha256)}</span> : null}
									</TableCell>
									<TableCell className="align-top text-xs whitespace-normal">
										<ArtifactAction file={file} exported={exported} runId={runId} sealed={sealed} sealError={sealError} />
									</TableCell>
								</TableRow>
							);
						})}
					</TableBody>
				</Table>
			</div>
		</section>
	);
}

function ArtifactAction({
	file,
	exported,
	runId,
	sealed,
	sealError,
}: {
	file: ArtifactFile;
	exported: boolean;
	runId: string;
	sealed: boolean;
	sealError?: string;
}) {
	const { t } = useTranslation("bench");
	if (!exported) {
		return (
			<span className="text-chart-error-ink break-words">{t("runs.scenario.artifacts.failed", { error: file.error || "missing" })}</span>
		);
	}
	if (!sealed) return <span className="text-muted-foreground">{t("runs.scenario.artifacts.unsealed")}</span>;
	if (sealError) return <span className="text-chart-error-ink break-words">{t("runs.scenario.artifacts.sealMismatch")}</span>;
	return (
		<a
			href={artifactDownloadUrl(runId, file)}
			download={file.name}
			rel="noopener noreferrer"
			aria-label={t("runs.scenario.artifacts.downloadAria", { name: file.name, artifact: file.id })}
			className="text-primary focus-visible:ring-ring/50 inline-flex items-center gap-1 rounded-sm font-medium underline-offset-2 hover:underline focus-visible:ring-[3px] focus-visible:outline-none"
			data-testid={`run-artifact-${file.id}-${file.name}-download`}
		>
			<Download className="size-3.5" aria-hidden="true" />
			{t("runs.scenario.artifacts.download")}
		</a>
	);
}