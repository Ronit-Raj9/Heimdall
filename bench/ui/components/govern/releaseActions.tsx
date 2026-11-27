import { JobRunButton } from "@/components/jobs/jobRunButton";
import { NativeSelect } from "@/components/ui/select";
import { useListCompliancePacksQuery } from "@/lib/store/apis/governApi";
import type { ReleaseRecord } from "@/lib/types/bench";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { CompliancePacks } from "./compliancePacks";
import { releaseAgent } from "./releaseAgent";
import { versionName } from "@/components/releases/releaseEvidence";

/**
 * A release as a select option: "codex v1, 21 Nov 2026" (M7-85). Falls back to the release id
 * when the record names no agent; a comparison release is named from its agent_config_id (Q-16). Exported for tests.
 */
export function releaseOptionLabel(
	r: Pick<ReleaseRecord, "release_id" | "agent_id" | "version" | "created_at"> & Partial<Pick<ReleaseRecord, "agent_config_id">>,
): string {
	const day = r.created_at ? new Date(r.created_at) : null;
	const date =
		day && !Number.isNaN(day.getTime()) ? day.toLocaleDateString("en-GB", { day: "numeric", month: "short", year: "numeric" }) : "";
	const a = releaseAgent({ agent_id: r.agent_id, version: r.version, agent_config_id: r.agent_config_id ?? "" });
	const name = a ? versionName(a.agent, a.version) : r.release_id;
	return date ? `${name}, ${date}` : name;
}

/** Release log actions as background jobs: verify the hash chain and evidence, write a release's compliance pack. */
export function ReleaseActions({ releases, onDone }: { releases: ReleaseRecord[]; onDone?(): void }) {
	const { t } = useTranslation("govern");
	const newest = releases.length > 0 ? releases[releases.length - 1].release_id : "";
	const [picked, setPicked] = useState("");
	const packs = useListCompliancePacksQuery();
	const releaseId = picked || newest;
	return (
		<div className="bg-card flex flex-col gap-5 rounded-xl border p-5" data-testid="release-actions">
			<h2 className="text-sm font-semibold">{t("releases.actions")}</h2>
			<div className="flex flex-col gap-1.5">
				<JobRunButton kind="release.verify" label={t("releases.verify")} variant="outline" onDone={() => onDone?.()} />
				<p className="text-muted-foreground line-clamp-2 text-xs">{t("releases.verifyHint")}</p>
			</div>
			{releases.length === 0 ? (
				<p className="text-muted-foreground text-xs" data-testid="release-actions-none">
					{t("releases.noReleases")}
				</p>
			) : (
				<div className="flex flex-col gap-2">
					<label className="text-muted-foreground flex flex-col gap-1 text-xs">
						{t("releases.pickRelease")}
						<NativeSelect
							value={releaseId}
							onChange={(e) => setPicked(e.target.value)}
							data-testid="release-actions-pick"
						>
							{[...releases].reverse().map((r) => (
								<option key={r.release_id} value={r.release_id}>
									{releaseOptionLabel(r)}
								</option>
							))}
						</NativeSelect>
					</label>
					<JobRunButton
						kind="release.compliance"
						params={{ release_id: releaseId }}
						label={t("releases.compliance")}
						variant="outline"
						onDone={() => void packs.refetch()}
					/>
					<p className="text-muted-foreground line-clamp-2 text-xs" title={t("releases.complianceHint")}>
						{t("releases.complianceHint")}
					</p>
				</div>
			)}
			<div className="border-t pt-5">
				<CompliancePacks />
			</div>
		</div>
	);
}
