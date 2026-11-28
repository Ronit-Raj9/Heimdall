import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { Badge } from "@/components/ui/badge";
import { NativeSelect } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatDateTime } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import { useGetLiveSessionsQuery } from "@/lib/store/apis/livescoreSessionsApi";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { LiveVersionView } from "./livescore.types";
import "./ufl9I18n";

/** The flags a scored session can carry (livescore/livescore.go); the server refuses any other. */
export const LIVE_FLAGS = [
	"abandoned",
	"request_error",
	"tool_error",
	"loop",
	"injection_marker",
	"budget_hit",
	"rate_limited",
	"refusal",
	"fallback",
	"cut",
] as const;

/** Rows asked per read; the server keeps the total. */
export const LIVE_SESSIONS_LIMIT = 50;

/** Every scored session, newest first, filtered by version and flag (GET /livescore/sessions). */
export function SessionsSection({ versions }: { versions: LiveVersionView[] }) {
	const { t } = useTranslation("ufl9");
	const locale = getLocale();
	const [version, setVersion] = useState("");
	const [flag, setFlag] = useState("");
	const q = useGetLiveSessionsQuery({ version, flag, limit: LIVE_SESSIONS_LIMIT });
	const filters = (
		<span className="flex flex-wrap items-center gap-2 text-xs">
			<label className="flex items-center gap-1">
				{t("sessions.version")}
				<NativeSelect value={version} onChange={(e) => setVersion(e.target.value)} data-testid="live-sessions-version">
					<option value="">{t("sessions.all")}</option>
					{versions.map((v) => (
						<option key={v.name} value={v.name}>
							{v.name}
						</option>
					))}
				</NativeSelect>
			</label>
			<label className="flex items-center gap-1">
				{t("sessions.flag")}
				<NativeSelect value={flag} onChange={(e) => setFlag(e.target.value)} data-testid="live-sessions-flag">
					<option value="">{t("sessions.all")}</option>
					{LIVE_FLAGS.map((f) => (
						<option key={f} value={f}>
							{f}
						</option>
					))}
				</NativeSelect>
			</label>
		</span>
	);
	const data = q.data;
	return (
		<Section title={t("sessions.title")} actions={filters} testId="live-sessions">
			{q.isLoading || q.isFetching ? (
				<BlockSkeleton rows={4} testId="live-sessions-loading" />
			) : q.error || !data ? (
				<LoadError title={t("sessions.loadError")} error={q.error} onRetry={() => void q.refetch()} testId="live-sessions-error" />
			) : data.sessions.length === 0 ? (
				<EmptyNote testId="live-sessions-empty">{t("sessions.empty")}</EmptyNote>
			) : (
				<>
					<div className="overflow-x-auto">
						<Table data-testid="live-sessions-table">
							<TableHeader>
								<TableRow>
									<TableHead>{t("sessions.columns.session")}</TableHead>
									<TableHead>{t("sessions.columns.version")}</TableHead>
									<TableHead>{t("sessions.columns.ended")}</TableHead>
									<TableHead>{t("sessions.columns.model")}</TableHead>
									<TableHead>{t("sessions.columns.flags")}</TableHead>
									<TableHead className="text-right">{t("sessions.columns.requests")}</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{data.sessions.map((s) => (
									<TableRow key={s.id} data-testid={`live-session-${s.id}`}>
										<TableCell className="font-mono text-xs">{s.id}</TableCell>
										<TableCell>{s.version}</TableCell>
										<TableCell>{formatDateTime(s.ended, locale)}</TableCell>
										<TableCell className="text-xs">{s.model ?? "-"}</TableCell>
										<TableCell>
											<span className="flex flex-wrap gap-1">
												{(s.flags ?? []).map((f) => (
													<Badge key={f} variant="outline">
														{f}
													</Badge>
												))}
											</span>
										</TableCell>
										<TableCell className="text-right tabular-nums">{s.requests}</TableCell>
									</TableRow>
								))}
							</TableBody>
						</Table>
					</div>
					<p className="text-muted-foreground text-xs" data-testid="live-sessions-count">
						{t("sessions.shown", { shown: data.sessions.length, total: data.total })}
					</p>
				</>
			)}
		</Section>
	);
}
