import { fileDiffAsRunDiff } from "@/components/agentdiff/agentDiff.utils";
import { DiffText } from "@/components/runs/diffText";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	type AgentVersion,
	type AgentVersionCreated,
	useCreateAgentVersionMutation,
	useGetAgentVersionQuery,
	useGetAgentVersionsQuery,
} from "@/lib/store/apis/evaluationsApi";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { lineDiff } from "./lineDiff";
import "./gw18I18n";

/** Short local date of a version, or the raw value when it does not parse. */
export function versionDate(value: string): string {
	const date = new Date(value);
	return Number.isNaN(date.getTime()) ? value : date.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

/** Versions newest first. */
export function sortVersions(versions: AgentVersion[]): AgentVersion[] {
	return [...versions].sort((x, y) => y.version - x.version);
}

/** What "Save as new version" does with the server's answer (N32): a new version clears the note and opens it; an
 * unchanged config made none, so the note stays in the box and the existing version opens. An answer without `created`
 * (an older server) counts as a new version. */
export function saveOutcome(data: AgentVersionCreated): {
	clearNote: boolean;
	select: number | undefined;
	message: { key: "versions.created" | "versions.unchanged"; version: number };
} {
	const created = data.created !== false;
	return {
		clearNote: created,
		select: data.version || undefined,
		message: { key: created ? "versions.created" : "versions.unchanged", version: data.version },
	};
}

/** The Author cell (N33): the display name the server sent, else the raw author; the raw author (an email only for
 * admins, the server leaves it out for everyone else) is the tooltip. */
export function authorOf(v: AgentVersion): { text: string; title: string | undefined } {
	return { text: v.created_by_name || v.created_by || "-", title: v.created_by || undefined };
}

/** The Versions tab: every saved version, its config read-only and what changed from the one before. */
export default function VersionsTab({ agentId }: { agentId: string }) {
	const { t } = useTranslation("agentBuilder");
	const { t: tg } = useTranslation("gw18");
	const versionsQuery = useGetAgentVersionsQuery(agentId);
	const [create, createState] = useCreateAgentVersionMutation();
	const [note, setNote] = useState("");
	const [selected, setSelected] = useState<number | null>(null);
	const [saved, setSaved] = useState<ReturnType<typeof saveOutcome>["message"] | null>(null);
	const versions = sortVersions(versionsQuery.data?.versions ?? []);

	const save = async () => {
		setSaved(null);
		const result = await create({ agent: agentId, note: note.trim() });
		if (!("error" in result) && result.data) {
			const outcome = saveOutcome(result.data);
			if (outcome.clearNote) setNote("");
			if (outcome.select) setSelected(outcome.select);
			setSaved(outcome.message);
		}
	};

	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="agent-versions-tab">
			<Section
				title={t("versions.title")}
				description={t("versions.description")}
				testId="agent-versions-section"
				actions={
					<form
						className="flex flex-wrap items-center gap-2"
						onSubmit={(e) => {
							e.preventDefault();
							void save();
						}}
					>
						<Input
							value={note}
							onChange={(e) => {
								setNote(e.target.value);
								setSaved(null);
							}}
							placeholder={t("versions.notePlaceholder")}
							aria-label={t("versions.note")}
							className="h-8 w-56"
							data-testid="agent-versions-note"
						/>
						<Button type="submit" size="sm" disabled={createState.isLoading} data-testid="agent-versions-save">
							{createState.isLoading ? t("versions.saving") : t("versions.save")}
						</Button>
					</form>
				}
			>
				{saved && (
					<p role="status" className="text-muted-foreground text-xs" data-testid="agent-versions-saved">
						{tg(saved.key, { n: saved.version })}
					</p>
				)}
				{createState.isError && (
					<p className="text-destructive text-xs" data-testid="agent-versions-save-error">
						{getBenchErrorMessage(createState.error)}
					</p>
				)}
				{versionsQuery.isLoading ? (
					<BlockSkeleton rows={4} testId="agent-versions-loading" />
				) : versionsQuery.isError ? (
					<LoadError
						title={t("versions.loadError")}
						error={versionsQuery.error}
						onRetry={() => void versionsQuery.refetch()}
						testId="agent-versions-error"
					/>
				) : versions.length === 0 ? (
					<EmptyNote testId="agent-versions-empty">{t("versions.empty")}</EmptyNote>
				) : (
					<Table data-testid="agent-versions-table">
						<TableHeader>
							<TableRow>
								<TableHead>{t("versions.col.version")}</TableHead>
								<TableHead>{t("versions.col.date")}</TableHead>
								<TableHead>{t("versions.col.author")}</TableHead>
								<TableHead>{t("versions.col.note")}</TableHead>
								<TableHead className="text-right">{t("versions.col.runs")}</TableHead>
								<TableHead />
							</TableRow>
						</TableHeader>
						<TableBody>
							{versions.map((v) => (
								<TableRow
									key={v.version}
									data-testid={`agent-version-v${v.version}`}
									data-state={selected === v.version ? "selected" : undefined}
								>
									<TableCell className="font-mono text-xs">v{v.version}</TableCell>
									<TableCell className="text-xs">{versionDate(v.created_at)}</TableCell>
									<TableCell className="text-xs" title={authorOf(v).title}>
										{authorOf(v).text}
									</TableCell>
									<TableCell className="max-w-[24rem] truncate text-xs">{v.note || "-"}</TableCell>
									<TableCell className="text-right text-xs tabular-nums">{v.runs ?? 0}</TableCell>
									<TableCell className="text-right">
										<Button
											size="sm"
											variant="ghost"
											onClick={() => setSelected(selected === v.version ? null : v.version)}
											data-testid={`agent-version-view-v${v.version}`}
										>
											{selected === v.version ? t("versions.hide") : t("versions.view")}
										</Button>
									</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
				)}
			</Section>
			{selected !== null && <VersionConfig agentId={agentId} version={selected} versions={versions} />}
		</div>
	);
}

/** One version's YAML (read-only) and its diff against the version just before it. */
function VersionConfig({ agentId, version, versions }: { agentId: string; version: number; versions: AgentVersion[] }) {
	const { t } = useTranslation("agentBuilder");
	const previous = versions
		.filter((v) => v.version < version)
		.map((v) => v.version)
		.sort((x, y) => y - x)[0];
	const current = useGetAgentVersionQuery({ agent: agentId, version });
	const before = useGetAgentVersionQuery(previous ? { agent: agentId, version: previous } : skipToken);
	const yaml = current.data?.yaml ?? "";
	const patch = previous && before.data ? lineDiff(before.data.yaml, yaml, { a: `v${previous}`, b: `v${version}` }) : "";
	return (
		<Section title={t("versions.configTitle", { version })} testId="agent-version-config">
			{current.isLoading ? (
				<BlockSkeleton rows={6} testId="agent-version-config-loading" />
			) : current.isError ? (
				<LoadError
					title={t("versions.configError")}
					error={current.error}
					onRetry={() => void current.refetch()}
					testId="agent-version-config-error"
				/>
			) : (
				<div className="grid min-w-0 gap-4 lg:grid-cols-2">
					<pre className="bench-mono-block max-h-[28rem] overflow-auto py-2 text-xs" tabIndex={0} data-testid="agent-version-yaml">
						{yaml}
					</pre>
					<div className="flex min-w-0 flex-col gap-2">
						<span className="text-muted-foreground text-xs font-medium">
							{previous ? t("versions.diffTitle", { previous, version }) : t("versions.first")}
						</span>
						{previous &&
							(before.isLoading ? (
								<BlockSkeleton rows={4} testId="agent-version-diff-loading" />
							) : (
								<DiffText runDiff={fileDiffAsRunDiff({ path: `agents/${agentId}.yaml`, diff: patch })} arm="agent-version" />
							))}
					</div>
				</div>
			)}
		</Section>
	);
}