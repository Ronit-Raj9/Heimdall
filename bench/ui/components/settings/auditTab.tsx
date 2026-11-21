import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { auditExportUrl, useVerifyAuditMutation } from "@/lib/store/apis/dataOpsApi";
import { useGetAuditLogQuery, useListMembersQuery } from "@/lib/store/apis/platformApi";
import type { AuditVerifyResult } from "@/lib/types/dataops";
import type { AuditEntry, AuditList, OrgSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Bot, ChevronLeft, ChevronRight, Cog, Download, KeyRound, Search, Server, ShieldAlert, ShieldCheck, UserRound, type LucideIcon } from "lucide-react";
import { useId, useMemo, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { SettingsSection, When } from "./settingsBits";

/** Rows per page of the audit log. */
export const AUDIT_PAGE_SIZE = 50;

const ACTOR_TYPES = ["user", "token", "runner", "system"] as const;
type ActorFilter = "all" | AuditEntry["actor_type"];
const ACTOR_ICON: Record<string, LucideIcon> = {
	user: UserRound,
	token: KeyRound,
	runner: Server,
	system: Bot,
};

/**
 * The cursor= of the page after this one: the server's opaque next cursor, or null on the last page.
 * A row timestamp would skip rows that share the last row's millisecond.
 */
export function nextAuditCursor(page: AuditList | undefined): string | null {
	return page?.next ? page.next : null;
}

/** Rows of one page that match the actor type and the free text (id, target, details). */
export function filterAuditRows(rows: readonly AuditEntry[], actor: ActorFilter, text: string): AuditEntry[] {
	const needle = text.trim().toLowerCase();
	return rows.filter((row) => {
		if (actor !== "all" && row.actor_type !== actor) return false;
		if (!needle) return true;
		const hay = [row.id, row.actor_id, row.action, row.target_type, row.target_id, row.ip, row.details ? JSON.stringify(row.details) : ""]
			.filter(Boolean)
			.join(" ")
			.toLowerCase();
		return hay.includes(needle);
	});
}

/** details as one short line of key=value pairs, for the table. */
export function detailsLine(details: AuditEntry["details"]): string {
	if (!details || typeof details !== "object") return "";
	return Object.entries(details)
		.map(([key, value]) => `${key}=${typeof value === "string" ? value : JSON.stringify(value)}`)
		.join("  ");
}

/** The chain head as a short hash for the result line. */
export function shortHash(hash: string): string {
	return hash.length > 12 ? hash.slice(0, 12) : hash;
}

/** The integrity check's answer: intact with the entry count and chain head, or where it breaks. */
export function AuditVerifyNote({ result }: { result: AuditVerifyResult }) {
	const { t } = useTranslation("platform");
	if (!result.ok) {
		return (
			<div
				role="alert"
				className="border-chart-error/40 bg-chart-error/[0.06] flex items-start gap-2 rounded-lg border px-3 py-2.5 text-sm"
				data-testid="audit-verify-broken"
			>
				<ShieldAlert className="text-chart-error-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
				<div className="flex flex-col gap-0.5">
					<span className="text-chart-error-ink font-medium">{t("settings.audit.verifyBroken", { seq: result.broken_at })}</span>
					{result.reason && <span className="text-muted-foreground text-xs">{t("settings.audit.verifyReason", { reason: result.reason })}</span>}
				</div>
			</div>
		);
	}
	return (
		<div className="flex items-start gap-2 rounded-lg border px-3 py-2.5 text-sm" role="status" data-testid="audit-verify-ok">
			<ShieldCheck className="text-chart-success-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
			<div className="flex flex-col gap-0.5">
				<span className="text-foreground">
					{t("settings.audit.verifyOk", { entries: result.entries, hash: shortHash(result.head_hash) })}
				</span>
				{result.legacy > 0 && (
					<span className="text-muted-foreground text-xs" data-testid="audit-verify-legacy">
						{t("settings.audit.verifyLegacy", { count: result.legacy })}
					</span>
				)}
			</div>
		</div>
	);
}

/** Export (JSONL, CSV) as plain downloads and the integrity check with its result inline. */
function AuditTools({ org }: { org: OrgSummary }) {
	const { t } = useTranslation("platform");
	const [verify, state] = useVerifyAuditMutation();
	return (
		<div className="flex flex-col gap-3" data-testid="audit-tools">
			<div className="flex flex-wrap items-center gap-2">
				<span className="text-muted-foreground mr-1 text-[13px] font-medium">{t("settings.audit.export")}</span>
				<Button asChild variant="outline" size="sm" className="gap-1">
					<a href={auditExportUrl(org.id, "jsonl")} download data-testid="audit-export-jsonl">
						<Download className="size-3.5" aria-hidden="true" />
						{t("settings.audit.exportJsonl")}
					</a>
				</Button>
				<Button asChild variant="outline" size="sm" className="gap-1">
					<a href={auditExportUrl(org.id, "csv")} download data-testid="audit-export-csv">
						<Download className="size-3.5" aria-hidden="true" />
						{t("settings.audit.exportCsv")}
					</a>
				</Button>
				<Button
					variant="outline"
					size="sm"
					className="ml-auto gap-1"
					isLoading={state.isLoading}
					onClick={() => void verify(org.id)}
					dataTestId="audit-verify"
				>
					<ShieldCheck className="size-3.5" aria-hidden="true" />
					{state.isLoading ? t("settings.audit.verifying") : t("settings.audit.verify")}
				</Button>
			</div>
			<p className="text-muted-foreground text-xs">{t("settings.audit.readsLogged")}</p>
			{state.data && <AuditVerifyNote result={state.data} />}
			{state.error && (
				<p role="alert" className="text-chart-error-ink text-xs" data-testid="audit-verify-error">
					{t("settings.audit.verifyError")}: {getBenchErrorMessage(state.error)}
				</p>
			)}
		</div>
	);
}

/**
 * The organization's audit log, newest first: who did what to which target, when and from where.
 * The action filter runs on the server; actor type and free text filter the page in view. Pages go
 * back in time with the server's opaque cursor.
 */
export function AuditTab({ org }: { org: OrgSummary }) {
	const { t } = useTranslation("platform");
	const formId = useId();
	const [actionInput, setActionInput] = useState("");
	const [action, setAction] = useState("");
	const [actor, setActor] = useState<ActorFilter>("all");
	const [text, setText] = useState("");
	// Cursors of the pages seen so far: [undefined] is the newest page.
	const [cursors, setCursors] = useState<(string | undefined)[]>([undefined]);
	const cursor = cursors[cursors.length - 1];
	const audit = useGetAuditLogQuery({
		orgId: org.id,
		action: action || undefined,
		cursor,
		limit: AUDIT_PAGE_SIZE,
	});
	// Users read as their email: the member list maps ids (both are admin routes, like this tab).
	const members = useListMembersQuery(org.id);
	const people = useMemo(() => {
		const byId: Record<string, string> = {};
		for (const member of members.data?.members ?? []) byId[member.user_id] = member.email;
		return byId;
	}, [members.data]);
	const rows = audit.data?.entries ?? [];
	const shown = useMemo(() => filterAuditRows(rows, actor, text), [rows, actor, text]);
	const next = nextAuditCursor(audit.data);

	const apply = (event: FormEvent) => {
		event.preventDefault();
		setAction(actionInput.trim());
		setCursors([undefined]);
	};
	const clear = () => {
		setActionInput("");
		setAction("");
		setActor("all");
		setText("");
		setCursors([undefined]);
	};

	return (
		<SettingsSection title={t("settings.audit.title")} description={t("settings.audit.description", { org: org.name })} testId="settings-audit">
			<AuditTools org={org} />
			<form className="flex flex-wrap items-end gap-3" onSubmit={apply} data-testid="audit-filters">
				<div className="flex min-w-48 flex-col gap-1.5">
					<label htmlFor={`${formId}-action`} className="text-[13px] font-medium">
						{t("settings.audit.filterAction")}
					</label>
					<Input
						id={`${formId}-action`}
						className="h-9 font-mono"
						placeholder={t("settings.audit.filterActionPlaceholder")}
						title={t("settings.audit.actionHint")}
						value={actionInput}
						onChange={(event) => setActionInput(event.target.value)}
						data-testid="audit-action"
					/>
				</div>
				<div className="flex flex-col gap-1.5">
					<span className="text-[13px] font-medium">{t("settings.audit.filterActorType")}</span>
					<Select value={actor} onValueChange={(value) => setActor(value as ActorFilter)}>
						<SelectTrigger className="min-w-32" aria-label={t("settings.audit.filterActorType")} data-testid="audit-actor">
							<SelectValue />
						</SelectTrigger>
						<SelectContent position="popper">
							<SelectItem value="all">{t("settings.audit.allActors")}</SelectItem>
							{ACTOR_TYPES.map((type) => (
								<SelectItem key={type} value={type}>
									{t(`settings.audit.actorTypes.${type}`)}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				</div>
				<div className="flex min-w-56 flex-1 flex-col gap-1.5">
					<label htmlFor={`${formId}-text`} className="text-[13px] font-medium">
						{t("settings.audit.filterText")}
					</label>
					<div className="relative">
						<Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2" aria-hidden="true" />
						<Input
							id={`${formId}-text`}
							className="h-9 pl-8"
							placeholder={t("settings.audit.filterTextPlaceholder")}
							value={text}
							onChange={(event) => setText(event.target.value)}
							data-testid="audit-text"
						/>
					</div>
				</div>
				<div className="flex gap-2">
					<Button type="submit" variant="outline" className="h-9" dataTestId="audit-apply">
						{t("settings.audit.apply")}
					</Button>
					<Button type="button" variant="ghost" className="h-9" onClick={clear} dataTestId="audit-clear">
						{t("settings.audit.clear")}
					</Button>
				</div>
			</form>

			{audit.isLoading ? (
				<BlockSkeleton rows={6} testId="audit-loading" />
			) : audit.isError || !audit.data ? (
				<LoadError title={t("settings.audit.loadError")} error={audit.error} onRetry={() => void audit.refetch()} testId="audit-error" />
			) : shown.length === 0 ? (
				<EmptyNote testId="audit-empty">{t("settings.audit.empty")}</EmptyNote>
			) : (
				<ol className={cn("relative flex flex-col", audit.isFetching && "opacity-60")} data-testid="audit-table">
					{shown.map((row, i) => {
						const Icon = ACTOR_ICON[row.actor_type] ?? Cog;
						const who =
							row.actor_type === "user" && row.actor_id && people[row.actor_id]
								? people[row.actor_id]
								: row.actor_id || t(`settings.audit.actorTypes.${row.actor_type}`);
						const target = `${row.target_type ? `${row.target_type}:` : ""}${row.target_id || ""}`;
						const details = detailsLine(row.details);
						return (
							<li key={row.id} className="relative flex gap-4 pb-6 last:pb-0" data-testid={`audit-row-${row.id}`}>
								{i < shown.length - 1 && <span className="bg-border absolute top-9 bottom-0 left-[17px] w-px" aria-hidden="true" />}
								<span className="bg-muted text-muted-foreground relative flex size-9 shrink-0 items-center justify-center rounded-full border">
									<Icon className="size-4" aria-hidden="true" />
								</span>
								<div className="flex min-w-0 flex-1 flex-col gap-1 pt-1">
									<div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
										<span className="text-foreground max-w-64 truncate font-medium" title={row.actor_id}>
											{who}
										</span>
										<code className="bg-muted text-foreground rounded px-1.5 py-0.5 font-mono text-xs">{row.action}</code>
										{target && (
											<span className="text-muted-foreground max-w-64 truncate font-mono text-xs" title={target}>
												{target}
											</span>
										)}
										<span className="text-muted-foreground ml-auto text-xs">
											<When value={row.at} />
										</span>
									</div>
									{(details || row.ip) && (
										<div className="text-muted-foreground flex min-w-0 flex-wrap gap-x-3 font-mono text-xs">
											{details && (
												<span className="max-w-full truncate" title={details}>
													{details}
												</span>
											)}
											{row.ip && <span>{row.ip}</span>}
										</div>
									)}
								</div>
							</li>
						);
					})}
				</ol>
			)}

			<nav className="flex items-center justify-between gap-2" aria-label={t("settings.audit.title")} data-testid="audit-paging">
				<span className="text-muted-foreground text-xs tabular-nums">{t("settings.audit.page", { n: cursors.length })}</span>
				<div className="flex gap-2">
					<Button
						variant="outline"
						size="sm"
						className="gap-1"
						disabled={cursors.length <= 1}
						onClick={() => setCursors((list) => list.slice(0, -1))}
						dataTestId="audit-newer"
					>
						<ChevronLeft className="size-3.5" aria-hidden="true" />
						{t("settings.audit.newer")}
					</Button>
					<Button
						variant="outline"
						size="sm"
						className="gap-1"
						disabled={next === null || audit.isFetching}
						onClick={() => next !== null && setCursors((list) => [...list, next])}
						dataTestId="audit-older"
					>
						{t("settings.audit.older")}
						<ChevronRight className="size-3.5" aria-hidden="true" />
					</Button>
				</div>
			</nav>
		</SettingsSection>
	);
}
