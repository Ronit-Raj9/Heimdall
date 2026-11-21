import { Button } from "@/components/ui/button";
import { compliancePackUrl, useListCompliancePacksQuery } from "@/lib/store/apis/governApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import type { CompliancePack } from "@/lib/types/govern";
import { Download } from "lucide-react";
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
			<h3 className="text-xs font-medium">{t("releases.packs.title")}</h3>
			{query.isLoading ? (
				<p className="text-muted-foreground text-xs">{t("releases.packs.loading")}</p>
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
				<ul className="divide-border divide-y rounded border" data-testid="compliance-packs-list">
					{groups.map((g) => (
						<li key={g.releaseId} className="flex flex-wrap items-center gap-2 p-2">
							<span className="font-mono text-xs">{g.releaseId}</span>
							<span className="ml-auto flex items-center gap-1">
								{g.files.map((f) => (
									<Button key={f.name} asChild size="sm" variant="outline" className="h-7 gap-1 px-2 text-xs">
										<a href={compliancePackUrl(f.name)} download={f.name} data-testid={`compliance-pack-download-${f.name}`}>
											<Download className="size-3.5" aria-hidden="true" />
											{f.format === "md" ? t("releases.packs.markdown") : t("releases.packs.json")}
										</a>
									</Button>
								))}
							</span>
						</li>
					))}
				</ul>
			)}
		</div>
	);
}
