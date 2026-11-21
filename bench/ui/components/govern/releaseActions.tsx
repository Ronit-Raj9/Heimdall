import { JobRunButton } from "@/components/jobs/jobRunButton";
import { Card, CardContent } from "@/components/ui/card";
import type { ReleaseRecord } from "@/lib/types/bench";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** Release log actions as background jobs: verify the hash chain and evidence, write a release's compliance pack. */
export function ReleaseActions({ releases, onDone }: { releases: ReleaseRecord[]; onDone?(): void }) {
	const { t } = useTranslation("govern");
	const newest = releases.length > 0 ? releases[releases.length - 1].release_id : "";
	const [picked, setPicked] = useState("");
	const releaseId = picked || newest;
	return (
		<Card data-testid="release-actions">
			<CardContent className="flex flex-col gap-4">
				<h2 className="text-sm font-medium">{t("releases.actions")}</h2>
				<div className="flex flex-col gap-1">
					<JobRunButton kind="release.verify" label={t("releases.verify")} variant="outline" onDone={() => onDone?.()} />
					<p className="text-muted-foreground text-xs">{t("releases.verifyHint")}</p>
				</div>
				{releases.length === 0 ? (
					<p className="text-muted-foreground text-xs" data-testid="release-actions-none">
						{t("releases.noReleases")}
					</p>
				) : (
					<div className="flex flex-col gap-2">
						<label className="flex flex-col gap-1 text-xs">
							{t("releases.pickRelease")}
							<select
								className="border-input bg-background h-9 rounded-md border px-2 font-mono text-xs"
								value={releaseId}
								onChange={(e) => setPicked(e.target.value)}
								data-testid="release-actions-pick"
							>
								{[...releases].reverse().map((r) => (
									<option key={r.release_id} value={r.release_id}>
										{r.release_id} ({r.exp_id})
									</option>
								))}
							</select>
						</label>
						<JobRunButton kind="release.compliance" params={{ release_id: releaseId }} label={t("releases.compliance")} variant="outline" />
						<p className="text-muted-foreground text-xs">{t("releases.complianceHint")}</p>
					</div>
				)}
			</CardContent>
		</Card>
	);
}
