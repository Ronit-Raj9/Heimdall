import { toDate } from "@/components/platform/platform.utils";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useGetAuditLogQuery, useListMembersQuery } from "@/lib/store/apis/platformApi";
import type { AuditEntry, OrgSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { ChevronLeft, ChevronRight, Search } from "lucide-react";
import { useId, useMemo, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { SettingsSection, TableFrame, When } from "./settingsBits";

/** Rows per page of the audit log. */
export const AUDIT_PAGE_SIZE = 50;

const ACTOR_TYPES = ["user", "token", "runner", "system"] as const;
type ActorFilter = "all" | AuditEntry["actor_type"];

/** The before= cursor for the page after rows: the oldest row's time in unix ms, or null on a short page. */
export function nextAuditCursor(rows: readonly AuditEntry[], pageSize: number): number | null {
	if (rows.length < pageSize) return null;
	const last = toDate(rows[rows.length - 1]?.at);
	return last ? last.getTime() : null;
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

/**
 * The organization's audit log, newest first: who did what to which target, when and from where.
 * The action filter runs on the server; actor type and free text filter the page in view. Pages go
 * back in time with the before= cursor.
 */
export function AuditTab({ org }: { org: OrgSummary }) {
	const { t } = useTranslation("platform");
	const formId = useId();
	const [actionInput, setActionInput] = useState("");
	const [action, setAction] = useState("");
	const [actor, setActor] = useState<ActorFilter>("all");
	const [text, setText] = useState("");
	// Cursors of the pages seen so far: [undefined] is the newest page.
	const [cursors, setCursors] = useState<(number | undefined)[]>([undefined]);
	const before = cursors[cursors.length - 1];
	const audit = useGetAuditLogQuery({ orgId: org.id, action: action || undefined, before, limit: AUDIT_PAGE_SIZE });
	// Users read as their email: the member list maps ids (both are admin routes, like this tab).
	const members = useListMembersQuery(org.id);
	const people = useMemo(() => {
		const byId: Record<string, string> = {};
		for (const member of members.data?.members ?? []) byId[member.user_id] = member.email;
		return byId;
	}, [members.data]);
	const rows = audit.data?.entries ?? [];
	const shown = useMemo(() => filterAuditRows(rows, actor, text), [rows, actor, text]);
	const next = nextAuditCursor(rows, AUDIT_PAGE_SIZE);

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
		<SettingsSection
			title={t("settings.audit.title")}
			description={t("settings.audit.description", { org: org.name })}
			testId="settings-audit"
		>
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
			</form>

			{audit.isLoading ? (
				<BlockSkeleton rows={6} testId="audit-loading" />
			) : audit.isError || !audit.data ? (
				<LoadError title={t("settings.audit.loadError")} error={audit.error} onRetry={() => void audit.refetch()} testId="audit-error" />
			) : shown.length === 0 ? (
				<EmptyNote testId="audit-empty">{t("settings.audit.empty")}</EmptyNote>
			) : (
				<TableFrame testId="audit-table">
					<Table stickyColumns={1} className={cn(audit.isFetching && "opacity-60")}>
						<TableHeader>
							<TableRow>
								<TableHead>{t("settings.audit.at")}</TableHead>
								<TableHead>{t("settings.audit.actor")}</TableHead>
								<TableHead>{t("settings.audit.action")}</TableHead>
								<TableHead>{t("settings.audit.target")}</TableHead>
								<TableHead>{t("settings.audit.ip")}</TableHead>
								<TableHead>{t("settings.audit.details")}</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{shown.map((row) => (
								<TableRow key={row.id} data-testid={`audit-row-${row.id}`}>
									<TableCell className="text-muted-foreground">
										<When value={row.at} />
									</TableCell>
									<TableCell>
										<span className="flex items-center gap-2">
											<Badge variant="outline" className="text-muted-foreground">
												{t(`settings.audit.actorTypes.${row.actor_type}`)}
											</Badge>
											{row.actor_type === "user" && row.actor_id && people[row.actor_id] ? (
												<span className="text-foreground max-w-56 truncate text-xs" title={row.actor_id}>
													{people[row.actor_id]}
												</span>
											) : (
												<span className="text-foreground max-w-48 truncate font-mono text-xs" title={row.actor_id}>
													{row.actor_id || "-"}
												</span>
											)}
										</span>
									</TableCell>
									<TableCell>
										<code className="bg-muted text-foreground rounded px-1.5 py-0.5 font-mono text-xs">{row.action}</code>
									</TableCell>
									<TableCell className="text-muted-foreground max-w-56 truncate font-mono text-xs" title={row.target_id}>
										{row.target_type ? `${row.target_type}:` : ""}
										{row.target_id || (row.target_type ? "" : "-")}
									</TableCell>
									<TableCell className="text-muted-foreground font-mono text-xs">{row.ip || "-"}</TableCell>
									<TableCell className="text-muted-foreground max-w-80 truncate font-mono text-xs" title={detailsLine(row.details)}>
										{detailsLine(row.details) || "-"}
									</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
				</TableFrame>
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