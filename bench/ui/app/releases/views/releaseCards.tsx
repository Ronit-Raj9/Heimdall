import { chainLinks, formatReleaseTime, newestFirst, shortHash } from "@/components/releases/releases.utils";
import { EntityLink } from "@/components/shell";
import { CopyId } from "@/components/ui/copyId";
import { releaseEvidenceUrl } from "@/lib/store/apis/benchApi";
import type { ReleaseRecord } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Bot, CheckCircle2, ChevronDown, Download, Link2Off, Rocket, ShieldAlert, ShieldCheck, TriangleAlert } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** Up to two initials from an approver name or email, for the avatar circle. */
export function initials(name: string): string {
	const base = name.split("@")[0] ?? "";
	const parts = base.split(/[\s._-]+/).filter(Boolean);
	if (parts.length === 0) return "?";
	if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
	return (parts[0][0] + parts[1][0]).toUpperCase();
}

/** The chain status banner: one line, green check or red shield. */
export function ChainBadge({ ok }: { ok: boolean }) {
	const { t } = useTranslation("u14");
	return (
		<span
			className={cn(
				"inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium",
				ok ? "bg-chart-success/15 text-chart-success-ink" : "bg-destructive/10 text-chart-error-ink",
			)}
			title={t("releases.tamperHint")}
			data-testid="release-chain-status"
			data-chain-ok={ok}
		>
			{ok ? <ShieldCheck className="size-3.5" aria-hidden="true" /> : <ShieldAlert className="size-3.5" aria-hidden="true" />}
			{ok ? t("releases.chainOk") : t("releases.chainBroken")}
		</span>
	);
}

/** Every release as a card on a vertical timeline, newest first. */
export function ReleaseCards({ releases, chainOk }: { releases: ReleaseRecord[]; chainOk: boolean }) {
	const links = chainLinks(releases);
	const ordered = newestFirst(releases);
	return (
		<ol className="relative flex flex-col gap-4 pl-6" data-testid="release-timeline" data-chain-ok={chainOk}>
			<span className="bg-border absolute top-2 bottom-2 left-[7px] w-px" aria-hidden="true" />
			{ordered.map((r) => (
				<ReleaseCard key={r.release_id} record={r} linked={links[r.release_id] ?? false} />
			))}
		</ol>
	);
}

function ReleaseCard({ record, linked }: { record: ReleaseRecord; linked: boolean }) {
	const { t } = useTranslation("u14");
	const [open, setOpen] = useState(false);
	const id = record.release_id;
	const ship = record.action === "ship";
	const agent = record.agent_config_id || `Arm ${record.arm}`;
	return (
		<li className="relative" data-testid={`release-entry-${id}`}>
			<span
				className={cn(
					"bg-background absolute top-5 -left-6 flex size-3.5 items-center justify-center rounded-full border-2",
					record.override ? "border-chart-error" : ship ? "border-chart-success" : "border-chart-warning",
				)}
				aria-hidden="true"
			/>
			<div className="bg-card rounded-xl border p-5 transition hover:shadow-md">
				<div className="flex flex-wrap items-start justify-between gap-3">
					<div className="flex min-w-0 items-center gap-3">
						<span className="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-lg">
							<Bot className="size-4.5" aria-hidden="true" />
						</span>
						<div className="min-w-0">
							<div className="truncate text-base font-semibold tracking-tight" title={agent} data-testid={`release-agent-${id}`}>
								{agent}
							</div>
							<time className="text-muted-foreground text-xs" dateTime={record.created_at} data-testid={`release-created-${id}`}>
								{formatReleaseTime(record.created_at)}
							</time>
						</div>
					</div>
					<div className="flex flex-wrap items-center gap-1.5">
						<span
							className={cn(
								"inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium",
								ship ? "bg-chart-success/15 text-chart-success-ink" : "bg-chart-warning/15 text-chart-warning-ink",
							)}
							title={record.decision || record.action}
							data-testid={`release-action-${id}`}
						>
							{ship ? <Rocket className="size-3" aria-hidden="true" /> : <TriangleAlert className="size-3" aria-hidden="true" />}
							{ship ? t("releases.shipped") : t("releases.held")}
						</span>
						{record.override && (
							<span
								className="bg-destructive/10 text-chart-error-ink inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium"
								title={t("releases.overrideHint")}
								data-testid={`release-override-tag-${id}`}
							>
								{t("releases.override")}
							</span>
						)}
					</div>
				</div>

				<div className="mt-4 flex flex-wrap items-center justify-between gap-3 text-sm">
					<span className="flex min-w-0 items-center gap-2" data-testid={`release-approver-${id}`}>
						<span
							className="bg-primary/10 text-primary flex size-7 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold"
							aria-hidden="true"
						>
							{initials(record.approver)}
						</span>
						<span className="text-muted-foreground truncate" title={record.approver}>
							{t("releases.approvedBy", { name: record.approver })}
						</span>
					</span>
					<span className="flex items-center gap-3 text-xs">
						<span
							className={cn("flex items-center gap-1", linked ? "text-chart-success-ink" : "text-chart-error-ink")}
							data-testid={`release-link-${id}`}
							data-linked={linked}
							title={record.hash}
						>
							{linked ? <CheckCircle2 className="size-3.5" aria-hidden="true" /> : <Link2Off className="size-3.5" aria-hidden="true" />}
							{linked ? t("releases.linked") : t("releases.unlinked")}
						</span>
						<a
							href={releaseEvidenceUrl(id)}
							download={`${id}-evidence.zip`}
							className="text-primary flex items-center gap-1 hover:underline"
							data-testid={`release-evidence-${id}`}
						>
							<Download className="size-3.5" aria-hidden="true" />
							{t("releases.evidence")}
						</a>
						<button
							type="button"
							className="text-muted-foreground hover:text-foreground flex items-center gap-0.5"
							onClick={() => setOpen((o) => !o)}
							aria-expanded={open}
							data-testid={`release-details-toggle-${id}`}
						>
							{t("releases.details")}
							<ChevronDown className={cn("size-3.5 transition", open && "rotate-180")} aria-hidden="true" />
						</button>
					</span>
				</div>

				{record.override && (
					<p className="text-chart-error-ink mt-3 line-clamp-2 text-sm wrap-anywhere" data-testid={`release-override-${id}`}>
						{record.override}
					</p>
				)}

				{open && (
					<dl
						className="bg-muted/40 mt-4 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-2 rounded-lg p-4 text-xs"
						data-testid={`release-details-${id}`}
					>
						<Row label={t("releases.experiment")}>
							<EntityLink kind="experiment" id={record.exp_id} className="text-xs" testId={`release-exp-${id}`} />
						</Row>
						<Row label={t("releases.config")}>
							<span className="font-mono" data-testid={`release-config-${id}`}>
								{record.arm} · {shortHash(record.config_hash)}
							</span>
						</Row>
						<Row label={t("releases.policy")}>
							<span data-testid={`release-policy-${id}`}>
								{record.policy_decision ? `${record.policy_name || "-"}: ${record.policy_decision}` : t("releases.noPolicy")}
							</span>
						</Row>
						{record.note && (
							<Row label={t("releases.note")}>
								<span className="wrap-anywhere" data-testid={`release-note-${id}`}>
									{record.note}
								</span>
							</Row>
						)}
						<Row label={t("releases.recordId")}>
							<CopyId value={id} testId={`release-id-${id}`} />
						</Row>
						<Row label={t("releases.hash")}>
							<CopyId value={record.hash} display={shortHash(record.hash)} />
						</Row>
						<Row label={t("releases.prevHash")}>
							{record.prev_hash ? <CopyId value={record.prev_hash} display={shortHash(record.prev_hash)} /> : <span>-</span>}
						</Row>
					</dl>
				)}
			</div>
		</li>
	);
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
	return (
		<>
			<dt className="text-muted-foreground">{label}</dt>
			<dd className="min-w-0 wrap-anywhere">{children}</dd>
		</>
	);
}
