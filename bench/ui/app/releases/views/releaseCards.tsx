import { chainLinks, formatReleaseTime, newestFirst, shortHash } from "@/components/releases/releases.utils";
import { ReleaseEvidence, releaseTitle } from "@/components/releases/releaseEvidence";
import { EntityLink } from "@/components/shell";
import { Link } from "@tanstack/react-router";
import { CopyId } from "@/components/ui/copyId";
import { releaseEvidenceUrl } from "@/lib/store/apis/benchApi";
import type { ReleaseRecord } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { CheckCircle2, ChevronDown, Download, Link2, Link2Off, Printer, ShieldAlert, ShieldCheck, TriangleAlert } from "lucide-react";
import "@/components/canaries/uf7bI18n";
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

/**
 * The override badge and line of a record (M7-75): one amber badge and a plain sentence, never red.
 * "unclear" when the test found no clear winner. Null for a record released without an override.
 * Exported for tests.
 */
export function overrideWords(record: Pick<ReleaseRecord, "override" | "action">): { unclear: boolean; reason: string } | null {
	if (!record.override) return null;
	return { unclear: record.action === "no_decision", reason: record.override };
}

/** The link that opens one release on the Releases page (M7-83). Exported for tests. */
export function releaseLink(origin: string, id: string): string {
	return `${origin}/releases?release=${encodeURIComponent(id)}`;
}

/** Marks the page for printing one release, prints, and clears the mark after the print dialog closes. */
export function printOneRelease(id: string, done: () => void, win: Window = window): void {
	const root = win.document.documentElement;
	const clear = () => {
		delete root.dataset.printRelease;
		win.removeEventListener("afterprint", clear);
		done();
	};
	root.dataset.printRelease = id;
	win.addEventListener("afterprint", clear);
	win.setTimeout(() => win.print(), 0);
}

function ReleaseCard({ record, linked }: { record: ReleaseRecord; linked: boolean }) {
	const { t } = useTranslation("u14");
	const { t: tg } = useTranslation("gw06");
	const { t: m } = useTranslation("uf7b");
	const [open, setOpen] = useState(false);
	const [copied, setCopied] = useState(false);
	const [printing, setPrinting] = useState(false);
	const id = record.release_id;
	const ship = record.action === "ship";
	// A record exists only because someone released, so the badge always says it was released (N171).
	// An override is one amber badge with the reason inline (M7-75); the advice shows when it was not ship.
	const advice = resultWords(record.action, tg as (key: string) => string);
	const over = overrideWords(record);
	const agent = releaseTitle(record, t as never);
	const first = !record.prev_hash || record.prev_hash === "-";
	const copyLink = () => {
		try {
			void navigator.clipboard?.writeText(releaseLink(window.location.origin, id)).then(() => setCopied(true), () => undefined);
		} catch {
			// No clipboard: nothing to do, the address bar still works.
		}
	};
	return (
		<li
			className={cn(
				"relative scroll-mt-4",
				// "Print this release" prints only this card: the others hide while the page is marked (M7-83).
				printing ? "print:break-inside-avoid" : "[html[data-print-release]_&]:print:hidden",
			)}
			id={`release-${id}`} data-testid={`release-entry-${id}`}>
			<span
				className={cn(
					"bg-background absolute top-5 -left-6 size-3.5 rounded-full border-2",
					over ? "border-chart-warning bg-chart-warning" : "border-chart-success bg-chart-success",
				)}
				aria-hidden="true"
			/>
			<div className="border-b pt-1 pb-5">
				<div className="flex flex-wrap items-start justify-between gap-3">
					<div className="min-w-0">
						<div className="truncate text-base font-semibold tracking-tight" title={agent} data-testid={`release-agent-${id}`}>
							{agent}
						</div>
						<time className="text-muted-foreground text-xs" dateTime={record.created_at} data-testid={`release-created-${id}`}>
							{formatReleaseTime(record.created_at)}
						</time>
					</div>
					<div className="flex flex-wrap items-center gap-1.5">
						{over ? (
							<span
								className="bg-chart-warning/15 text-chart-warning-ink inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium"
								data-testid={`release-override-tag-${id}`}
							>
								<TriangleAlert className="size-3" aria-hidden="true" />
								{over.unclear ? t("releases.overrideUnclear") : t("releases.override")}
							</span>
						) : (
							<>
								<span
									className="bg-chart-success/15 text-chart-success-ink inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium"
									data-testid={`release-action-${id}`}
								>
									{t("releases.shipped")}
								</span>
								{!ship && (
									<span
										className="bg-chart-warning/15 text-chart-warning-ink inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium"
										data-testid={`release-advice-${id}`}
									>
										<TriangleAlert className="size-3" aria-hidden="true" />
										{tg("releases.resultSaid", { action: advice })}
									</span>
								)}
							</>
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
					<span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
						<span
							className={cn("flex min-h-6 items-center gap-1", linked ? "text-chart-success-ink" : "text-chart-error-ink")}
							data-testid={`release-link-${id}`}
							data-linked={linked}
						>
							{linked ? <CheckCircle2 className="size-3.5" aria-hidden="true" /> : <Link2Off className="size-3.5" aria-hidden="true" />}
							{!linked ? t("releases.unlinked") : first ? t("releases.linkedFirst") : t("releases.linked")}
						</span>
						<a
							href={releaseEvidenceUrl(id)}
							download={`${id}-evidence.zip`}
							className="text-primary flex min-h-6 items-center gap-1 hover:underline"
							data-testid={`release-evidence-${id}`}
						>
							<Download className="size-3.5" aria-hidden="true" />
							{t("releases.evidence")}
						</a>
						<button
							type="button"
							className="text-primary flex min-h-6 items-center gap-1 hover:underline"
							onClick={copyLink}
							data-testid={`release-copy-link-${id}`}
						>
							<Link2 className="size-3.5" aria-hidden="true" />
							{copied ? m("releases.copied") : m("releases.copyLink")}
						</button>
						<button
							type="button"
							className="text-primary flex min-h-6 items-center gap-1 hover:underline print:hidden"
							onClick={() => {
								setOpen(true);
								setPrinting(true);
								printOneRelease(id, () => setPrinting(false));
							}}
							data-testid={`release-print-${id}`}
						>
							<Printer className="size-3.5" aria-hidden="true" />
							{m("releases.print")}
						</button>
						<button
							type="button"
							className="text-muted-foreground hover:text-foreground flex min-h-6 items-center gap-0.5"
							onClick={() => setOpen((o) => !o)}
							aria-expanded={open}
							data-testid={`release-details-toggle-${id}`}
						>
							{t("releases.details")}
							<ChevronDown className={cn("size-3.5 transition", open && "rotate-180")} aria-hidden="true" />
						</button>
					</span>
				</div>

				{record.summary ? (
					<div className="bg-muted/30 mt-4 rounded-lg px-3 py-2" data-testid={`release-evidence-strip-${id}`}>
						<ReleaseEvidence summary={record.summary} testId={`release-numbers-${id}`} />
					</div>
				) : (
					// M7-76: a record made from a comparison has no numbers of its own; say so and link to them.
					<p className="text-muted-foreground mt-4 text-sm" data-testid={`release-no-numbers-${id}`}>
						{m("releases.noNumbers")}{" "}
						{record.evaluation_id ? (
							<Link to="/evaluations/$evalId" params={{ evalId: record.evaluation_id }} className="text-primary hover:underline">
								{m("releases.openComparison")}
							</Link>
						) : (
							<EntityLink kind="experiment" id={record.exp_id} className="text-sm">
								{m("releases.openComparison")}
							</EntityLink>
						)}
					</p>
				)}

				{over && (
					<p className="mt-3 text-sm wrap-anywhere" data-testid={`release-override-${id}`}>
						{over.unclear ? t("releases.overrideLineUnclear") : t("releases.overrideLine", { action: advice })}{" "}
						{t("releases.reason", { reason: over.reason })}
					</p>
				)}

				{open && (
					<dl
						className="bg-muted/40 mt-4 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-2 rounded-lg p-4 text-xs"
						data-testid={`release-details-${id}`}
					>
						{record.evaluation_id ? (
							<>
								<Row label={t("releases.evaluation")}>
									<Link
										to="/evaluations/$evalId"
										params={{ evalId: record.evaluation_id }}
										className="text-primary text-xs hover:underline"
										data-testid={`release-evaluation-${id}`}
									>
										{record.evaluation_id}
									</Link>
								</Row>
								<Row label={t("releases.benchmarks")}>
									<span className="flex flex-wrap gap-x-3 gap-y-1">
										{(record.children ?? []).map((c) => (
											<EntityLink key={c.exp_id} kind="experiment" id={c.exp_id} className="text-xs">
												{c.benchmark}
											</EntityLink>
										))}
									</span>
								</Row>
								{record.gate && (
									<Row label={t("releases.gate")}>
										<span data-testid={`release-gate-${id}`}>{record.gate}</span>
									</Row>
								)}
							</>
						) : (
							<Row label={t("releases.experiment")}>
								<EntityLink kind="experiment" id={record.exp_id} className="text-xs" testId={`release-exp-${id}`} />
							</Row>
						)}
						{record.agent_id && record.version ? (
							<Row label={m("releases.version")}>
								<span data-testid={`release-version-${id}`}>{`${record.agent_id} v${record.version}`}</span>
							</Row>
						) : null}
						<Row label={m("releases.arm")}>
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

/** The result's advice in plain words ("hold", "no decision"); an unknown value as written. */
function resultWords(action: string, tg: (key: string) => string): string {
	return ["ship", "hold", "no_decision", "fix_harness"].includes(action) ? tg(`releases.action.${action}`) : action;
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
	return (
		<>
			<dt className="text-muted-foreground">{label}</dt>
			<dd className="min-w-0 wrap-anywhere">{children}</dd>
		</>
	);
}