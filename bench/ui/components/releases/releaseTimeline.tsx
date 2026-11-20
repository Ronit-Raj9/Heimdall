import { Badge } from "@/components/ui/badge";
import { EntityLink } from "@/components/shell";
import { releaseEvidenceUrl } from "@/lib/store/apis/benchApi";
import type { ReleaseRecord } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Download, Link2, Link2Off, ShieldAlert, ShieldCheck } from "lucide-react";
import { useTranslation } from "react-i18next";
import { chainLinks, formatReleaseTime, newestFirst, shortHash } from "./releases.utils";

export interface ReleaseTimelineProps {
	/** Log order, oldest first, as GET /releases returns them. */
	releases: ReleaseRecord[];
	chainOk: boolean;
}

/** Every recorded release, newest first, with the chain status of the log and of each link. */
export function ReleaseTimeline({ releases, chainOk }: ReleaseTimelineProps) {
	const { t } = useTranslation("bench");
	const links = chainLinks(releases);
	const ordered = newestFirst(releases);

	return (
		<div className="flex flex-col gap-3" data-testid="release-timeline">
			<div
				className={cn(
					"flex items-start gap-2 rounded-sm border px-3 py-2 text-sm",
					chainOk ? "border-border" : "border-destructive/40 bg-destructive/5",
				)}
				data-testid="release-chain-status"
				data-chain-ok={chainOk}
			>
				{chainOk ? (
					<ShieldCheck className="text-chart-success-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
				) : (
					<ShieldAlert className="text-chart-error-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
				)}
				<div className="flex flex-col gap-0.5">
					<span className="font-medium">{chainOk ? t("releases.timeline.chainOk") : t("releases.timeline.chainBroken")}</span>
					{!chainOk && <span className="text-muted-foreground text-xs">{t("releases.timeline.chainBrokenHint")}</span>}
				</div>
			</div>
			{ordered.length === 0 ? (
				<p
					className="text-muted-foreground rounded-sm border border-dashed px-4 py-6 text-center text-sm"
					data-testid="release-timeline-empty"
				>
					{t("releases.timeline.empty")}
				</p>
			) : (
				<ol className="flex flex-col gap-3">
					{ordered.map((record) => (
						<ReleaseEntry key={record.release_id} record={record} linked={links[record.release_id] ?? false} />
					))}
				</ol>
			)}
		</div>
	);
}

function ReleaseEntry({ record, linked }: { record: ReleaseRecord; linked: boolean }) {
	const { t } = useTranslation("bench");
	const id = record.release_id;
	return (
		<li
			className={cn("min-w-0 rounded-sm border px-4 py-3 text-sm", record.override && "border-l-chart-error border-l-4")}
			data-testid={`release-entry-${id}`}
		>
			<div className="flex flex-wrap items-baseline justify-between gap-2">
				<span className="min-w-0 font-mono text-xs break-all" data-testid={`release-id-${id}`}>
					{id}
				</span>
				<time className="text-muted-foreground text-xs" dateTime={record.created_at} data-testid={`release-created-${id}`}>
					{formatReleaseTime(record.created_at)}
				</time>
			</div>
			<dl className="mt-2 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1">
				<Row label={t("releases.timeline.experiment")}>
					<EntityLink kind="experiment" id={record.exp_id} className="text-xs" testId={`release-exp-${id}`} />
				</Row>
				<Row label={t("releases.timeline.config")}>
					<span className="break-all" data-testid={`release-config-${id}`}>
						{t("releases.timeline.configValue", {
							arm: record.arm,
							id: record.agent_config_id || "-",
							hash: shortHash(record.config_hash),
						})}
					</span>
				</Row>
				<Row label={t("releases.timeline.verdict")}>
					<span className="flex flex-wrap gap-1.5">
						<Badge variant="outline" data-testid={`release-decision-${id}`}>
							{record.decision}
						</Badge>
						<Badge variant={record.action === "ship" ? "success" : "warning"} data-testid={`release-action-${id}`}>
							{record.action}
						</Badge>
					</span>
				</Row>
				<Row label={t("releases.timeline.policy")}>
					<span data-testid={`release-policy-${id}`}>
						{record.policy_decision
							? t("releases.timeline.policyValue", { name: record.policy_name || "-", decision: record.policy_decision })
							: t("releases.timeline.noPolicy")}
					</span>
				</Row>
				<Row label={t("releases.timeline.approver")}>
					<span data-testid={`release-approver-${id}`}>{record.approver}</span>
				</Row>
				{record.note && (
					<Row label={t("releases.timeline.note")}>
						<span className="break-words" data-testid={`release-note-${id}`}>
							{record.note}
						</span>
					</Row>
				)}
				{record.override && (
					<Row label={t("releases.timeline.override")}>
						<span className="text-chart-error-ink font-medium break-words" data-testid={`release-override-${id}`}>
							{record.override}
						</span>
					</Row>
				)}
			</dl>
			<div className="mt-3 flex flex-wrap items-center justify-between gap-2 text-xs">
				<span
					className={cn("flex items-center gap-1", linked ? "text-muted-foreground" : "text-chart-error-ink")}
					data-testid={`release-link-${id}`}
					data-linked={linked}
					title={t("releases.timeline.hashTitle", { hash: record.hash, prev: record.prev_hash || "-" })}
				>
					{linked ? <Link2 className="size-3.5" aria-hidden="true" /> : <Link2Off className="size-3.5" aria-hidden="true" />}
					{linked ? t("releases.timeline.linked") : t("releases.timeline.linkBroken")}
					<span className="font-mono">{shortHash(record.hash)}</span>
				</span>
				<a
					href={releaseEvidenceUrl(id)}
					download={`${id}-evidence.zip`}
					className="text-primary flex items-center gap-1 hover:underline"
					data-testid={`release-evidence-${id}`}
				>
					<Download className="size-3.5" aria-hidden="true" />
					{t("releases.timeline.evidence")}
				</a>
			</div>
		</li>
	);
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
	return (
		<>
			<dt className="text-muted-foreground text-xs leading-5">{label}</dt>
			<dd className="min-w-0">{children}</dd>
		</>
	);
}