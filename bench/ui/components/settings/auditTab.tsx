import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { auditExportUrl, useVerifyAuditMutation } from "@/lib/store/apis/dataOpsApi";
import { useGetAuditLogQuery, useListMembersQuery } from "@/lib/store/apis/platformApi";
import type { AuditVerifyResult } from "@/lib/types/dataops";
import type { AuditEntry, AuditList, Member, OrgSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import {
	Bot,
	ChevronLeft,
	ChevronRight,
	Cog,
	Download,
	KeyRound,
	Search,
	Server,
	ShieldAlert,
	ShieldCheck,
	UserRound,
	type LucideIcon,
} from "lucide-react";
import { useEffect, useId, useMemo, useState, type FormEvent } from "react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { formatWhen } from "../platform/platform.utils";
import { useTranslation } from "react-i18next";
import { SettingsSection, When } from "./settingsBits";
import "./w4m8I18n";
import "./uf8aI18n";

/** Rows per page of the audit log. */
export const AUDIT_PAGE_SIZE = 50;

const ACTOR_TYPES = ["user", "token", "runner", "system", "anonymous"] as const;
type ActorFilter = "all" | AuditEntry["actor_type"];
const ACTOR_ICON: Record<string, LucideIcon> = {
	user: UserRound,
	token: KeyRound,
	runner: Server,
	system: Bot,
};

/** A translate function loose enough for keys built at run time. */
type AuditT = (key: string, options?: { ns?: string; defaultValue?: string }) => string;

/** The actor type in plain words: a server value the page does not know shows as itself, never as a key path. */
export function actorTypeLabel(t: AuditT, type: string): string {
	if (type === "anonymous") return t("audit.anonymous", { ns: "w4m8" });
	return t(`settings.audit.actorTypes.${type}`, { ns: "platform", defaultValue: type });
}

/** Who did it: a user by their member name (never an address, "Someone" when unknown), anyone else by id or type. */
export function auditActorName(t: AuditT, row: AuditEntry, members: readonly Pick<Member, "user_id" | "name">[]): string {
	if (row.actor_type === "user") {
		const name = members.find((member) => member.user_id === row.actor_id)?.name?.trim();
		return name || t("audit.someone", { ns: "w4m8" });
	}
	return row.actor_id || actorTypeLabel(t, row.actor_type);
}

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
					{result.reason && (
						<span className="text-muted-foreground text-xs">{t("settings.audit.verifyReason", { reason: result.reason })}</span>
					)}
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
function AuditTools({ org, filters }: { org: OrgSummary; filters: { action?: string; actor?: string; q?: string } }) {
	const { t } = useTranslation("platform");
	const { t: tm } = useTranslation("w4m8");
	const filtered = Boolean(filters.action || filters.actor || filters.q?.trim());
	const [verify, state] = useVerifyAuditMutation();
	return (
		<div className="flex flex-col gap-3" data-testid="audit-tools">
			<div className="flex flex-wrap items-center gap-2">
				<span className="text-muted-foreground mr-1 text-[13px] font-medium">{t("settings.audit.export")}</span>
				<Button asChild variant="outline" size="sm" className="gap-1">
					<a href={auditExportUrl(org.id, "jsonl", filters)} download data-testid="audit-export-jsonl">
						<Download className="size-3.5" aria-hidden="true" />
						{filtered ? tm("audit.exportJsonlMatch") : t("settings.audit.exportJsonl")}
					</a>
				</Button>
				<Button asChild variant="outline" size="sm" className="gap-1">
					<a href={auditExportUrl(org.id, "csv", filters)} download data-testid="audit-export-csv">
						<Download className="size-3.5" aria-hidden="true" />
						{filtered ? tm("audit.exportCsvMatch") : t("settings.audit.exportCsv")}
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

/** Action groups of the filter, by the first part of an action id ("member.role" is Members). */
export const AUDIT_GROUPS = {
	members: ["member", "members", "invite", "invites", "invitation", "invitations"],
	keys: ["key", "keys", "model", "models", "token", "tokens", "secret", "secrets", "runner_token", "runner_tokens"],
	releases: ["release", "releases"],
	runs: ["run", "runs", "experiment", "experiments", "evaluation", "evaluations", "runner", "runners"],
	settings: ["org", "orgs", "settings", "retention", "billing", "prompt_logs", "github", "integration", "integrations"],
} as const;
export type AuditGroup = keyof typeof AUDIT_GROUPS;

/** True for an entry that only read something (its action ends in ".read" or ".list"). */
export function isReadAction(action: string): boolean {
	return /\.(read|list|view|export)$/.test(action);
}

/** The group of an action id, or undefined when it is in none. */
export function auditGroupOf(action: string): AuditGroup | undefined {
	const head = action.split(".")[0] ?? "";
	return (Object.keys(AUDIT_GROUPS) as AuditGroup[]).find((g) => (AUDIT_GROUPS[g] as readonly string[]).includes(head));
}

/** Rows kept by the page's own filters: changes only unless reads are included, and the action group. */
export function filterAuditView(rows: readonly AuditEntry[], opts: { reads: boolean; group: AuditGroup | "all" }): AuditEntry[] {
	return rows.filter(
		(row) => (opts.reads || !isReadAction(row.action)) && (opts.group === "all" || auditGroupOf(row.action) === opts.group),
	);
}

/** The verb of an action id in words: its last part ("member.role" is "role", "runs.diff.read" is "read"). */
const VERBS = [
	"read",
	"list",
	"view",
	"export",
	"create",
	"update",
	"delete",
	"revoke",
	"role",
	"register",
	"invite",
	"accept",
	"remove",
	"rename",
	"login",
	"logout",
] as const;

/**
 * One entry as a sentence: "{{actor}} opened diff of runs r_1". The verb comes from the action's
 * last part, the object from the parts before it; an action the page does not know falls back to
 * "{{actor}} did <action>".
 */
export function auditSentence(t: AuditT, row: AuditEntry, actor: string): string {
	const parts = row.action.split(".");
	const verb = parts[parts.length - 1] ?? "";
	const what = parts.slice(0, -1).join(" ").replace(/_/g, " ");
	const target = row.target_id || "";
	const vars = { actor, what, target } as unknown as { defaultValue?: string };
	const text = (VERBS as readonly string[]).includes(verb)
		? t(`audit.verbs.${verb}${target ? "" : "NoTarget"}`, { ns: "uf8a", ...vars })
		: t("audit.verbs.other", { ns: "uf8a", ...vars, action: row.action } as unknown as { defaultValue?: string });
	return text.replace(/\s+/g, " ").trim();
}

/** Older than a day: the absolute time reads better than "3 weeks ago". */
export function auditAbsolute(at: AuditEntry["at"], now = Date.now()): boolean {
	const ms = typeof at === "string" ? Date.parse(at) : typeof at === "number" ? at : NaN;
	return Number.isFinite(ms) && now - ms > 24 * 3600 * 1000;
}

function AuditTime({ at }: { at: AuditEntry["at"] }) {
	const { i18n } = useTranslation();
	if (!auditAbsolute(at)) return <When value={at} />;
	const abs = formatWhen(at, i18n.language) ?? "";
	return <time title={abs}>{abs}</time>;
}

/**
 * The organization's audit log, newest first: who did what to which target, when and from where,
 * as sentences with the raw action, route and address behind "Details". Changes only by default.
 * Actor type, action group and free text filter the page in view and live in the URL; pages go back
 * in time with the server's opaque cursor.
 */
export function AuditTab({ org }: { org: OrgSummary }) {
	const { t } = useTranslation("platform");
	const { t: tf } = useTranslation("uf8a");
	const formId = useId();
	const search = useSearch({ strict: false }) as { q?: unknown; actor?: unknown; group?: unknown; reads?: unknown };
	const navigate = useNavigate();
	const actor: ActorFilter =
		typeof search.actor === "string" && (ACTOR_TYPES as readonly string[]).includes(search.actor) ? (search.actor as ActorFilter) : "all";
	const group: AuditGroup | "all" = typeof search.group === "string" && search.group in AUDIT_GROUPS ? (search.group as AuditGroup) : "all";
	const reads = search.reads === "1" || search.reads === 1 || search.reads === true;
	const urlText = typeof search.q === "string" ? search.q : "";
	const [text, setText] = useState(urlText);
	useEffect(() => setText(urlText), [urlText]);
	const setSearch = (patch: Record<string, string | undefined>) =>
		void navigate({ search: ((prev: Record<string, unknown>) => ({ ...prev, ...patch })) as never, replace: true });
	// Cursors of the pages seen so far: [undefined] is the newest page.
	const [cursors, setCursors] = useState<(string | undefined)[]>([undefined]);
	const cursor = cursors[cursors.length - 1];
	const audit = useGetAuditLogQuery({
		orgId: org.id,
		cursor,
		limit: AUDIT_PAGE_SIZE,
	});
	// Users read as their member name, never their address (both are admin routes, like this tab).
	const members = useListMembersQuery(org.id);
	const people = members.data?.members ?? [];
	const tl = t as unknown as AuditT;
	const tx = tf as unknown as AuditT;
	const rows = audit.data?.entries ?? [];
	const shown = useMemo(
		() => filterAuditView(filterAuditRows(rows, actor, urlText), { reads, group }),
		[rows, actor, urlText, reads, group],
	);
	const next = nextAuditCursor(audit.data);
	const filtered = actor !== "all" || group !== "all" || Boolean(urlText.trim());

	const apply = (event: FormEvent) => {
		event.preventDefault();
		setSearch({ q: text.trim() || undefined });
		setCursors([undefined]);
	};
	const clear = () => {
		setText("");
		setSearch({ q: undefined, actor: undefined, group: undefined });
		setCursors([undefined]);
	};

	return (
		<SettingsSection
			title={t("settings.audit.title")}
			description={t("settings.audit.description", { org: org.name })}
			testId="settings-audit"
		>
			<AuditTools org={org} filters={{ actor: actor === "all" ? undefined : actor, q: urlText }} />
			<form className="flex flex-wrap items-end gap-3" onSubmit={apply} data-testid="audit-filters">
				<div className="flex flex-col gap-1.5">
					<span className="text-[13px] font-medium">{t("settings.audit.filterAction")}</span>
					<Select value={group} onValueChange={(value) => setSearch({ group: value === "all" ? undefined : value })}>
						<SelectTrigger className="min-w-36" aria-label={t("settings.audit.filterAction")} data-testid="audit-action">
							<SelectValue />
						</SelectTrigger>
						<SelectContent position="popper">
							<SelectItem value="all">{tf("audit.groups.all")}</SelectItem>
							{(Object.keys(AUDIT_GROUPS) as AuditGroup[]).map((g) => (
								<SelectItem key={g} value={g}>
									{tf(`audit.groups.${g}`)}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				</div>
				<div className="flex flex-col gap-1.5">
					<span className="text-[13px] font-medium">{t("settings.audit.filterActorType")}</span>
					<Select value={actor} onValueChange={(value) => setSearch({ actor: value === "all" ? undefined : value })}>
						<SelectTrigger className="min-w-32" aria-label={t("settings.audit.filterActorType")} data-testid="audit-actor">
							<SelectValue />
						</SelectTrigger>
						<SelectContent position="popper">
							<SelectItem value="all">{t("settings.audit.allActors")}</SelectItem>
							{ACTOR_TYPES.map((type) => (
								<SelectItem key={type} value={type}>
									{actorTypeLabel(tl, type)}
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
						<Search
							className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2"
							aria-hidden="true"
						/>
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
				<label className="flex h-9 items-center gap-2 text-sm">
					<input
						type="checkbox"
						checked={reads}
						onChange={(event) => setSearch({ reads: event.target.checked ? "1" : undefined })}
						data-testid="audit-reads"
					/>
					{tf("audit.includeReads")}
				</label>
			</form>
			<p className="text-muted-foreground text-xs">{tf("audit.pageOnly")}</p>

			{audit.isLoading ? (
				<BlockSkeleton rows={6} testId="audit-loading" />
			) : audit.isError || !audit.data ? (
				<LoadError title={t("settings.audit.loadError")} error={audit.error} onRetry={() => void audit.refetch()} testId="audit-error" />
			) : rows.length === 0 ? (
				<EmptyNote testId="audit-empty">{t("settings.audit.empty")}</EmptyNote>
			) : shown.length === 0 ? (
				<div className="flex flex-wrap items-center gap-3" data-testid="audit-no-match">
					<EmptyNote testId="audit-empty">{filtered ? tf("audit.noMatch") : tf("audit.onlyReads")}</EmptyNote>
					{filtered ? (
						<Button type="button" size="sm" variant="outline" onClick={clear} dataTestId="audit-no-match-clear">
							{tf("audit.clearFilters")}
						</Button>
					) : (
						!reads && (
							<Button type="button" size="sm" variant="outline" onClick={() => setSearch({ reads: "1" })} dataTestId="audit-no-match-reads">
								{tf("audit.includeReads")}
							</Button>
						)
					)}
				</div>
			) : (
				<ol className={cn("relative flex flex-col", audit.isFetching && "opacity-60")} data-testid="audit-table">
					{shown.map((row, i) => {
						const Icon = ACTOR_ICON[row.actor_type] ?? Cog;
						const who = auditActorName(tl, row, people);
						const target = `${row.target_type ? `${row.target_type}:` : ""}${row.target_id || ""}`;
						const details = detailsLine(row.details);
						return (
							<li key={row.id} className="relative flex gap-4 pb-6 last:pb-0" data-testid={`audit-row-${row.id}`}>
								{i < shown.length - 1 && <span className="bg-border absolute top-9 bottom-0 left-[17px] w-px" aria-hidden="true" />}
								<span className="bg-muted text-muted-foreground relative flex size-9 shrink-0 items-center justify-center rounded-full border">
									<Icon className="size-4" aria-hidden="true" />
								</span>
								<div className="flex min-w-0 flex-1 flex-col gap-1 pt-1">
									<div className="flex flex-wrap items-baseline gap-x-2 gap-y-1 text-sm">
										<span className="text-foreground min-w-0 break-words" title={row.actor_id}>
											{auditSentence(tx, row, who)}
										</span>
										<span className="text-muted-foreground ml-auto text-xs">
											<AuditTime at={row.at} />
										</span>
									</div>
									<details className="text-muted-foreground text-xs">
										<summary className="w-fit cursor-pointer select-none">{tf("audit.details")}</summary>
										<div className="mt-1 flex min-w-0 flex-wrap gap-x-3 gap-y-1 font-mono">
											<code className="bg-muted text-foreground rounded px-1.5 py-0.5">{row.action}</code>
											{target && (
												<span className="max-w-full truncate" title={target}>
													{target}
												</span>
											)}
											{details && (
												<span className="max-w-full truncate" title={details}>
													{details}
												</span>
											)}
											{row.ip && <span>{row.ip}</span>}
										</div>
									</details>
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