import { useConfirm } from "@/components/platform/confirmDialog";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { InlineError } from "@/components/runs/screenStates";
import { notify } from "@/lib/notify";
import { canUse, useSession } from "@/lib/session/sessionContext";
import {
	badgeMarkdown,
	reportExportUrl,
	reproduceUrl,
	shareUrl,
	useCreateShareMutation,
	useListSharesQuery,
	useRevokeShareMutation,
	type Share,
	type ShareKind,
} from "@/lib/store/apis/shareApi";
import "@/lib/types/shareI18n";
import "@/components/settings/w4m8I18n";
import "./uf8bI18n";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { cn } from "@/lib/utils";
import { Check, Copy, Download, FileCode2, FileText, Link2, Package, Printer, Share2 } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** Expiry choices, in days; 0 is never. */
export const EXPIRY_CHOICES = [1, 7, 30, 90, 0] as const;

export interface ShareTarget {
	kind: ShareKind;
	target: string;
	/** The view's filters as a URL query (report card: version, model; leaderboard: its filters). */
	query?: string;
	/** Set for an evaluation: offers the HTML, Markdown and PDF downloads and the reproduce bundle. */
	evaluationId?: string;
}

/** Turns a filter object into the query a link carries, dropping empty values. */
export function shareQuery(params: Record<string, string | number | undefined | null>): string {
	const q = new URLSearchParams();
	for (const [k, v] of Object.entries(params)) {
		if (v !== undefined && v !== null && String(v).trim() !== "") q.set(k, String(v));
	}
	q.sort();
	return q.toString();
}

/** A query with its keys in sorted order, the way the server stores a link's query. */
export function canonicalQuery(q?: string): string {
	const p = new URLSearchParams(q ?? "");
	p.sort();
	return p.toString();
}

/** The "what they will see" lines under the intro, in order (M8-69). Exported for tests. */
export function shareAudienceLines(kind: ShareKind, hidePrompts: boolean): string[] {
	return [`share.visible.${kind}`, hidePrompts ? "share.hiddenPrompts" : "share.hidden", "share.live", "share.pageUrl"];
}

/** Active links first; turned off and expired ones only when asked for (M8-30). Exported for tests. */
export function splitLinks(shares: Share[]): { active: Share[]; off: Share[] } {
	return { active: shares.filter((s) => s.state === "active"), off: shares.filter((s) => s.state !== "active") };
}

/** A link as the server may now send it: `created_by_name` is new and may be missing (G1, M8-72). */
type ShareWithName = Share & { created_by_name?: string };

/** Who made a link: the member's display name when the server sends it, else the stored id. Exported for tests. */
export function creatorName(s: Share): string {
	const name = (s as ShareWithName).created_by_name?.trim();
	return name || s.created_by || "";
}

/** The line under the downloads: the PDF hint, or what to do when the PDF could not be prepared. */
export function PdfHint({ failed }: { failed: boolean }) {
	const { t } = useTranslation("share");
	const { t: tm } = useTranslation("w4m8");
	if (failed) {
		return (
			<p role="alert" className="text-destructive text-xs" data-testid="share-pdf-failed">
				{tm("share.pdfFailed")}
			</p>
		);
	}
	return <p className="text-muted-foreground text-xs">{t("pdfHint")}</p>;
}

/** Loads an HTML export and opens the print dialog on it (Save as PDF), without leaving the page. */
export async function printExport(url: string): Promise<boolean> {
	try {
		const res = await fetch(url, { credentials: "same-origin" });
		if (!res.ok) return false;
		const html = await res.text();
		const frame = document.createElement("iframe");
		frame.setAttribute("aria-hidden", "true");
		frame.style.cssText = "position:fixed;right:0;bottom:0;width:0;height:0;border:0;visibility:hidden";
		frame.srcdoc = html;
		document.body.appendChild(frame);
		await new Promise<void>((resolve) => {
			frame.onload = () => resolve();
		});
		frame.contentWindow?.focus();
		frame.contentWindow?.print();
		window.setTimeout(() => frame.remove(), 60_000);
		return true;
	} catch {
		return false;
	}
}

function CopyButton({ text, testId }: { text: string; testId: string }) {
	const { t } = useTranslation("share");
	const [state, setState] = useState<"" | "ok" | "fail">("");
	const copy = async () => {
		try {
			await navigator.clipboard.writeText(text);
			setState("ok");
			notify({ text: t("copied", { ns: "toast" }) });
		} catch {
			setState("fail");
		}
		window.setTimeout(() => setState(""), 2000);
	};
	return (
		<Button type="button" variant="outline" size="sm" onClick={copy} dataTestId={testId} aria-live="polite">
			{state === "ok" ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
			{state === "ok" ? t("copied") : state === "fail" ? t("copyFailed") : t("copy")}
		</Button>
	);
}

function formatDate(iso?: string): string {
	if (!iso) return "";
	const d = new Date(iso);
	return Number.isNaN(d.getTime())
		? iso
		: d.toLocaleDateString(undefined, {
				year: "numeric",
				month: "short",
				day: "numeric",
			});
}

/** The Share button that opens the dialog. Put it in a page header or toolbar. */
export function ShareButton({ className, label, ...target }: ShareTarget & { className?: string; /** Button text; default "Share" (M4-112). */ label?: string }) {
	const { t } = useTranslation("share");
	const [open, setOpen] = useState(false);
	return (
		<>
			<Button type="button" variant="outline" size="sm" className={className} onClick={() => setOpen(true)} dataTestId="share-open">
				<Share2 aria-hidden="true" />
				{label ?? t("button")}
			</Button>
			{open && <ShareDialog {...target} onClose={() => setOpen(false)} />}
		</>
	);
}

function Row({ icon, title, help, children }: { icon: ReactNode; title: string; help?: string; children: ReactNode }) {
	return (
		<div className="flex flex-wrap items-center gap-x-3 gap-y-2 py-2.5">
			<span className="text-muted-foreground flex size-8 shrink-0 items-center justify-center rounded-lg border">{icon}</span>
			{/* On a phone the title takes its own line, so the buttons wrap under it instead of into it (M8-76). */}
			<div className="min-w-0 flex-1 basis-[calc(100%-3rem)] sm:basis-auto">
				<p className="text-sm font-medium">{title}</p>
				{help && <p className="text-muted-foreground text-xs">{help}</p>}
			</div>
			<div className="flex shrink-0 flex-wrap gap-2">{children}</div>
		</div>
	);
}

/** The share dialog: make a read-only link, list and turn off links to this item, downloads. */
export function ShareDialog({ kind, target, query, evaluationId, onClose }: ShareTarget & { onClose(): void }) {
	const { t } = useTranslation("share");
	const { t: tu } = useTranslation("uf8b");
	const session = useSession();
	const confirm = useConfirm();
	const canCreate = canUse(session, "member");
	const [days, setDays] = useState<number>(30);
	const [hidePrompts, setHidePrompts] = useState(false);
	const [label, setLabel] = useState("");
	const [created, setCreated] = useState<Share | null>(null);
	const [pdfFailed, setPdfFailed] = useState(false);
	const [showOff, setShowOff] = useState(false);
	const [revokeError, setRevokeError] = useState<unknown>(null);
	const links = useListSharesQuery({ kind, target });
	const [create, createState] = useCreateShareMutation();
	const [revoke] = useRevokeShareMutation();
	// Only links with the same view filters belong to this exact view.
	// The server stores the query with its keys sorted, so both sides are compared in that order.
	const mine = (links.data?.shares ?? []).filter((s) => canonicalQuery(s.query) === canonicalQuery(query));
	const { active, off } = splitLinks(mine);
	const listed = showOff ? [...active, ...off] : active;

	const submit = async () => {
		const res = await create({
			kind,
			target,
			query,
			label: label.trim() || undefined,
			hide_prompts: hidePrompts,
			expires_in_days: days,
		});
		if ("data" in res && res.data) setCreated(res.data);
	};

	const turnOff = async (s: Share) => {
		const name = s.label || formatDate(s.created_at);
		const expiry = s.expires_at ? t("expires", { when: formatDate(s.expires_at) }) : tu("share.neverExpiresLower");
		if (
			!(await confirm(tu("share.revokeConfirmNamed", { name, expiry }), {
				destructive: true,
				confirmLabel: t("revoke"),
			}))
		)
			return;
		setRevokeError(null);
		try {
			await revoke(s.id).unwrap();
			if (created?.id === s.id) setCreated(null);
			notify({ text: t("revoked", { ns: "toast" }) });
		} catch (e) {
			setRevokeError(e);
		}
	};

	const newUrl = created ? shareUrl(created) : "";
	const showBadge = kind === "evaluation" || kind === "report_card" || kind === "verdict";

	return (
		<Dialog
			open
			onOpenChange={(o) => {
				if (!o) onClose();
			}}
			title={t("title", { what: kind === "verdict" ? tu("share.whatVerdict") : t(`what.${kind}`) })}
			description={
				<>
					<p>{t("intro")}</p>
					<ul className="mt-2 flex list-disc flex-col gap-1 pl-4 text-xs" data-testid="share-audience">
						{shareAudienceLines(kind, hidePrompts).map((k) => (
							<li key={k}>{tu(k as "share.live")}</li>
						))}
					</ul>
				</>
			}
			closeLabel={t("close")}
			testId="share-dialog"
			footer={
				<Button type="button" variant="outline" onClick={onClose} dataTestId="share-close-footer">
					{tu("share.cancel")}
				</Button>
			}
		>
				<div className="flex flex-col gap-5">
					{canCreate ? (
						<section className="flex flex-col gap-3" data-testid="share-create">
							<fieldset className="flex flex-col gap-2">
								<legend className="text-sm font-medium">{t("expiry")}</legend>
								<div className="bg-muted/60 inline-flex w-fit flex-wrap gap-1 rounded-lg p-1" role="radiogroup">
									{EXPIRY_CHOICES.map((d) => (
										<button
											key={d}
											type="button"
											role="radio"
											aria-checked={days === d}
											onClick={() => setDays(d)}
											data-testid={`share-expiry-${d}`}
											className={cn(
												"rounded-md px-2.5 py-1 text-xs font-medium transition-colors",
												days === d ? "bg-background text-foreground shadow-xs" : "text-muted-foreground hover:text-foreground",
											)}
										>
											{t(`expiryOption.${d}`)}
										</button>
									))}
								</div>
							</fieldset>
							<label className="flex items-start gap-2.5 text-sm">
								<input
									type="checkbox"
									className="accent-primary mt-0.5 size-4"
									checked={hidePrompts}
									onChange={(e) => setHidePrompts(e.target.checked)}
									data-testid="share-hide-prompts"
								/>
								<span>
									{t("hidePrompts")}
									<span className="text-muted-foreground block text-xs">{t("hidePromptsHelp")}</span>
								</span>
							</label>
							<label className="flex flex-col gap-1.5 text-sm">
								<span className="font-medium">{t("label")}</span>
								<Input
									value={label}
									maxLength={120}
									placeholder={t("labelPlaceholder")}
									onChange={(e) => setLabel(e.target.value)}
									data-testid="share-label"
								/>
							</label>
							<div className="flex items-center gap-3">
								<Button type="button" onClick={submit} isLoading={createState.isLoading} dataTestId="share-create-submit">
									<Link2 aria-hidden="true" />
									{t("create")}
								</Button>
								{createState.isError && (
									<p role="alert" className="text-destructive text-sm">
										{tu("share.createError", { message: getBenchErrorMessage(createState.error) })}
									</p>
								)}
							</div>
							{created && newUrl && (
								<div className="bg-muted/50 flex flex-col gap-2 rounded-lg border p-3" data-testid="share-created">
									<div className="flex items-center gap-2">
										<Input
											readOnly
											value={newUrl}
											onFocus={(e) => e.currentTarget.select()}
											className="font-mono text-xs"
											data-testid="share-created-url"
										/>
										<CopyButton text={newUrl} testId="share-copy-url" />
									</div>
									{showBadge && created.token && (
										<div className="flex flex-col gap-1.5">
											<p className="text-xs font-medium">{t("badge")}</p>
											<div className="flex items-center gap-2">
												<Input
													readOnly
													value={badgeMarkdown(created.token, newUrl)}
													onFocus={(e) => e.currentTarget.select()}
													className="font-mono text-xs"
												/>
												<CopyButton text={badgeMarkdown(created.token, newUrl)} testId="share-copy-badge" />
											</div>
											<p className="text-muted-foreground text-xs">{t("badgeHelp")}</p>
										</div>
									)}
								</div>
							)}
						</section>
					) : (
						<p className="text-muted-foreground text-sm" data-testid="share-read-only">
							{t("readOnlyRole")}
						</p>
					)}

					<section className="flex flex-col gap-2" data-testid="share-links">
						<h3 className="text-sm font-medium">{t("links")}</h3>
						{mine.length === 0 ? (
							<p className="text-muted-foreground text-sm">{t("noLinks")}</p>
						) : (
							<>
								{listed.length > 0 && (
									<ul className="divide-border/80 flex flex-col divide-y rounded-lg border">
										{listed.map((s) => (
											<li key={s.id} className="flex flex-wrap items-center gap-2 px-3 py-2 text-sm" data-testid={`share-link-${s.id}`}>
												<span
													className={cn(
														"rounded-full px-2 py-0.5 text-xs font-medium",
														s.state === "active" ? "bg-chart-success/10 text-chart-success-ink" : "bg-muted text-muted-foreground",
													)}
												>
													{t(`state.${s.state}`)}
												</span>
												<span className="flex min-w-0 flex-1 flex-col">
													<span className="truncate">{s.label || formatDate(s.created_at)}</span>
													<span className="text-muted-foreground truncate text-xs" data-testid={`share-link-by-${s.id}`}>
														{creatorName(s)
															? tu("share.by", { name: creatorName(s), created: formatDate(s.created_at) })
															: tu("share.created", { created: formatDate(s.created_at) })}
													</span>
												</span>
												<span className="text-muted-foreground text-xs">
													{s.expires_at ? t("expires", { when: formatDate(s.expires_at) }) : t("neverExpires")}
												</span>
												{s.state === "active" && shareUrl(s) && <CopyButton text={shareUrl(s)} testId={`share-copy-${s.id}`} />}
												{s.state === "active" && canCreate && (
													<Button type="button" variant="ghost" size="sm" onClick={() => void turnOff(s)} dataTestId={`share-revoke-${s.id}`}>
														{t("revoke")}
													</Button>
												)}
											</li>
										))}
									</ul>
								)}
								{off.length > 0 && (
									<Button
										type="button"
										variant="ghost"
										size="sm"
										className="w-fit"
										onClick={() => setShowOff((v) => !v)}
										aria-expanded={showOff}
										dataTestId="share-show-off"
									>
										{showOff ? tu("share.hideOff") : tu("share.showOff", { count: off.length })}
									</Button>
								)}
							</>
						)}
						<InlineError error={revokeError} title={tu("share.revokeFailed")} testId="share-revoke-error" />
					</section>

					{evaluationId && (
						<section className="flex flex-col" data-testid="share-downloads">
							<h3 className="text-sm font-medium">{t("downloads")}</h3>
							<Row icon={<FileText className="size-4" aria-hidden="true" />} title={tu("share.reportCard")}>
								<Button asChild variant="outline" size="sm">
									<a href={reportExportUrl(evaluationId, "html")} download data-testid="share-download-html">
										<Download aria-hidden="true" />
										HTML
									</a>
								</Button>
								<Button asChild variant="outline" size="sm">
									<a href={reportExportUrl(evaluationId, "md")} download data-testid="share-download-md">
										<FileCode2 aria-hidden="true" />
										{t("markdown")}
									</a>
								</Button>
								<Button
									type="button"
									variant="outline"
									size="sm"
									dataTestId="share-download-pdf"
									onClick={async () => setPdfFailed(!(await printExport(reportExportUrl(evaluationId, "html"))))}
								>
									<Printer aria-hidden="true" />
									{t("pdf")}
								</Button>
							</Row>
							<PdfHint failed={pdfFailed} />
							<Row icon={<Package className="size-4" aria-hidden="true" />} title={t("reproduce")} help={t("reproduceHelp")}>
								<Button asChild variant="outline" size="sm">
									<a href={reproduceUrl(evaluationId)} download data-testid="share-download-reproduce">
										<Download aria-hidden="true" />
										.zip
									</a>
								</Button>
							</Row>
						</section>
					)}
				</div>
		</Dialog>
	);
}
