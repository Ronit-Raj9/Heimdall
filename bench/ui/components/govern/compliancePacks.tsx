import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { compliancePackUrl, useListCompliancePacksQuery } from "@/lib/store/apis/governApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import type { CompliancePack } from "@/lib/types/govern";
import { Download, FileJson, FileText } from "lucide-react";
import { useTranslation } from "react-i18next";

/** Compliance packs grouped by release, newest first: one row per release with a Download button per file. */
export function groupPacks(packs: CompliancePack[]): { releaseId: string; files: CompliancePack[] }[] {
	const out: { releaseId: string; files: CompliancePack[] }[] = [];
	for (const p of packs) {
		const g = out.find((x) => x.releaseId === p.release_id);
		if (g) g.files.push(p);
		else out.push({ releaseId: p.release_id, files: [p] });
	}
	for (const g of out) g.files.sort((a, b) => a.format.localeCompare(b.format));
	return out;
}

/** The compliance packs the release.compliance job wrote (GET /compliance-packs), each file downloadable. */
export function CompliancePacks() {
	const { t } = useTranslation("govern");
	const query = useListCompliancePacksQuery();
	const groups = groupPacks(query.data?.packs ?? []);
	return (
		<div className="flex flex-col gap-2" data-testid="compliance-packs">
			<h3 className="text-sm font-semibold">{t("releases.packs.title")}</h3>
			{query.isLoading ? (
				<div className="flex gap-2">
					<Skeleton className="h-8 w-28 rounded-lg" />
					<Skeleton className="h-8 w-28 rounded-lg" />
				</div>
			) : query.isError ? (
				<div className="flex flex-wrap items-center gap-2">
					<p role="alert" className="text-chart-error-ink text-xs" data-testid="compliance-packs-error">
						{t("releases.packs.loadError")}: {getBenchErrorMessage(query.error)}
					</p>
					<Button size="sm" variant="ghost" className="h-7 px-2 text-xs" onClick={() => void query.refetch()}>
						{t("releases.packs.retry")}
					</Button>
				</div>
			) : groups.length === 0 ? (
				<p className="text-muted-foreground text-xs" data-testid="compliance-packs-empty">
					{t("releases.packs.empty")}
				</p>
			) : (
				<ul className="flex flex-col gap-3" data-testid="compliance-packs-list">
					{groups.map((g) => (
						<li key={g.releaseId} className="flex flex-col gap-1.5">
							<span className="text-muted-foreground truncate font-mono text-[11px]" title={g.releaseId}>
								{g.releaseId}
							</span>
							<span className="flex flex-wrap gap-2">
								{g.files.map((f) => (
									<a
										key={f.name}
										href={compliancePackUrl(f.name)}
										download={f.name}
										title={f.name}
										className="bg-muted/50 hover:bg-muted hover:border-primary/40 group inline-flex items-center gap-2 rounded-lg border px-2.5 py-1.5 text-xs transition"
										data-testid={`compliance-pack-download-${f.name}`}
									>
										{f.format === "md" ? (
											<FileText className="text-chart-info-ink size-4" aria-hidden="true" />
										) : (
											<FileJson className="text-chart-warning-ink size-4" aria-hidden="true" />
										)}
										<span className="font-medium">{f.format === "md" ? t("releases.packs.markdown") : t("releases.packs.json")}</span>
										<Download className="text-muted-foreground group-hover:text-foreground size-3.5" aria-hidden="true" />
									</a>
								))}
							</span>
						</li>
					))}
				</ul>
			)}
		</div>
	);
}
