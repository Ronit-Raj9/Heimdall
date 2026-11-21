import { JobRunButton } from "@/components/jobs/jobRunButton";
import { EmptyState } from "@/components/shell/emptyState";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	useDeleteAgentSkillMutation,
	useGetAgentMCPQuery,
	useGetAgentSkillsQuery,
	usePutAgentMCPMutation,
	useUploadAgentSkillMutation,
} from "@/lib/store/apis/agentopsApi";
import { MCP_FORMATS, scanMCPJob, scanSkillJob, type AgentMCPResponse } from "@/lib/types/agentops";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { type AgentOpsTabProps, Field, OpsSection, useAgentId } from "./agentOps.shared";

const MAX_ZIP_MB = 50;
const SKILL_NAME = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;

const SERVERS_EXAMPLE = `services:
  - id: files
    image: ghcr.io/example/mcp-files@sha256:<digest>
    port: 8080
    mcp:
      transport: http
      path: /mcp
`;

/** A skill name from a zip file name: review.zip gives review. */
export function skillNameFromFile(file: string): string {
	return file
		.replace(/\.zip$/i, "")
		.replace(/[^A-Za-z0-9._-]+/g, "-")
		.replace(/^[^A-Za-z0-9]+/, "")
		.slice(0, 64);
}

/** The Tools tab of an agent: its skills (zip upload, scan, delete) and its MCP tools. */
export function ToolsTab(props: AgentOpsTabProps = {}) {
	const { t } = useTranslation("agentops");
	const id = useAgentId(props);
	if (!id) return <EmptyState compact message={t("tools.noAgent")} action={{ label: t("tools.agents"), to: "/agents" }} />;
	return (
		<div className="space-y-4" data-testid="agent-tools-tab">
			<SkillsSection id={id} />
			<MCPSection id={id} />
		</div>
	);
}

function SkillsSection({ id }: { id: string }) {
	const { t } = useTranslation("agentops");
	const { data, error, isLoading } = useGetAgentSkillsQuery(id);
	const [upload, { isLoading: uploading }] = useUploadAgentSkillMutation();
	const [remove] = useDeleteAgentSkillMutation();
	const [file, setFile] = useState<File | null>(null);
	const [name, setName] = useState("");
	const [replace, setReplace] = useState(false);
	const [message, setMessage] = useState<{ ok: boolean; text: string } | null>(null);

	const pick = (f: File | null) => {
		setFile(f);
		setMessage(null);
		if (f && !name) setName(skillNameFromFile(f.name));
	};
	const problem = !file
		? t("tools.skills.needZip")
		: file.size > MAX_ZIP_MB << 20
			? t("tools.skills.tooLarge", { mb: MAX_ZIP_MB })
			: !SKILL_NAME.test(name)
				? t("tools.skills.badName")
				: null;
	const submit = async () => {
		if (!file || problem) return;
		const res = await upload({ id, name, file, replace });
		if ("error" in res && res.error) setMessage({ ok: false, text: getBenchErrorMessage(res.error) });
		else {
			setMessage({ ok: true, text: t("tools.skills.uploaded", { name }) });
			setFile(null);
			setName("");
		}
	};
	const del = async (skill: string) => {
		if (!window.confirm(t("tools.skills.confirmDelete", { name: skill }))) return;
		const res = await remove({ id, name: skill });
		if ("error" in res && res.error) setMessage({ ok: false, text: getBenchErrorMessage(res.error) });
	};

	return (
		<OpsSection
			title={t("tools.skills.title")}
			hint={t("tools.skills.hint", { dir: data?.dir ?? `agents/${id}/skills` })}
			testId="agent-skills-section"
		>
			{isLoading ? (
				<p className="text-muted-foreground text-xs">{t("tools.skills.loading")}</p>
			) : error ? (
				<p className="text-destructive text-xs" data-testid="agent-skills-error">
					{getBenchErrorMessage(error)}
				</p>
			) : data && data.skills.length > 0 ? (
				<ul className="divide-border divide-y rounded border" data-testid="agent-skills-list">
					{data.skills.map((s) => (
						<li key={s.name} className="flex flex-wrap items-center gap-3 p-2 text-sm">
							<span className="font-mono">{s.name}</span>
							<span className="text-muted-foreground text-xs">
								{t("tools.skills.size", { files: s.files, kib: (s.bytes / 1024).toFixed(1) })}
							</span>
							{!s.valid && <Badge variant="destructive">{t("tools.skills.noSkillMd")}</Badge>}
							<span className="ml-auto flex items-center gap-2">
								<JobRunButton {...scanSkillJob(s.path)} label={t("tools.skills.scan")} variant="outline" />
								<Button size="sm" variant="ghost" onClick={() => void del(s.name)} data-testid={`agent-skill-delete-${s.name}`}>
									{t("tools.skills.delete")}
								</Button>
							</span>
						</li>
					))}
				</ul>
			) : (
				<p className="text-muted-foreground text-xs" data-testid="agent-skills-empty">
					{t("tools.skills.empty")}
				</p>
			)}
			{data && data.skills.length > 0 && !data.installed && (
				<p className="text-xs text-amber-600">{t("tools.skills.notInstalled", { dir: data.dir })}</p>
			)}
			<div className="flex flex-wrap items-end gap-3">
				<Field label={t("tools.skills.zip")}>
					<input type="file" accept=".zip,application/zip" onChange={(e) => pick(e.target.files?.[0] ?? null)} data-testid="agent-skill-file" />
				</Field>
				<Field label={t("tools.skills.name")}>
					<Input value={name} onChange={(e) => setName(e.target.value)} placeholder="code-review" data-testid="agent-skill-name" />
				</Field>
				<label className="flex items-center gap-2 pb-2 text-xs">
					<input type="checkbox" checked={replace} onChange={(e) => setReplace(e.target.checked)} />
					{t("tools.skills.replace")}
				</label>
				<Button disabled={!!problem || uploading} onClick={() => void submit()} data-testid="agent-skill-upload">
					{uploading ? t("tools.skills.uploading") : t("tools.skills.upload")}
				</Button>
			</div>
			{file && problem && <p className="text-destructive text-xs">{problem}</p>}
			{message && (
				<p className={message.ok ? "text-xs text-green-700" : "text-destructive text-xs"} data-testid="agent-skill-message">
					{message.text}
				</p>
			)}
		</OpsSection>
	);
}

function MCPSection({ id }: { id: string }) {
	const { t } = useTranslation("agentops");
	const { data, error, isLoading } = useGetAgentMCPQuery(id);
	const [save, { isLoading: saving }] = usePutAgentMCPMutation();
	const [form, setForm] = useState(() => ({ format: data?.format ?? "", mount_path: data?.mount_path ?? "", servers_yaml: data?.servers_yaml ?? "" }));
	const [message, setMessage] = useState<{ ok: boolean; text: string } | null>(null);

	useEffect(() => {
		if (data) setForm({ format: data.format, mount_path: data.mount_path, servers_yaml: data.servers_yaml });
	}, [data]);

	const dirty = !!data && (form.format !== data.format || form.mount_path !== data.mount_path || form.servers_yaml !== data.servers_yaml);
	const submit = async () => {
		const res = await save({ id, body: form });
		if ("error" in res && res.error) setMessage({ ok: false, text: getBenchErrorMessage(res.error) });
		else setMessage({ ok: true, text: t("tools.mcp.saved") });
	};

	return (
		<OpsSection
			title={t("tools.mcp.title")}
			hint={t("tools.mcp.hint")}
			testId="agent-mcp-section"
		>
			{isLoading ? (
				<p className="text-muted-foreground text-xs">{t("tools.mcp.loading")}</p>
			) : error ? (
				<p className="text-destructive text-xs" data-testid="agent-mcp-error">
					{getBenchErrorMessage(error)}
				</p>
			) : (
				<>
					<div className="grid gap-3 sm:grid-cols-2">
						<Field label={t("tools.mcp.format")}>
							<select
								className="border-input bg-background h-9 rounded-md border px-2 text-sm"
								value={form.format}
								onChange={(e) => setForm({ ...form, format: e.target.value })}
								data-testid="agent-mcp-format"
							>
								{MCP_FORMATS.map((f) => (
									<option key={f} value={f}>
										{f || t("tools.mcp.adapterDefault")}
									</option>
								))}
							</select>
						</Field>
						<Field label={t("tools.mcp.path")}>
							<Input
								value={form.mount_path}
								onChange={(e) => setForm({ ...form, mount_path: e.target.value })}
								placeholder="/root/.codex/config.toml"
								data-testid="agent-mcp-path"
							/>
						</Field>
					</div>
					<Field label={t("tools.mcp.servers", { file: data?.servers_file ?? `agents/${id}/mcp.yaml` })}>
						<textarea
							className="border-input bg-background min-h-40 rounded-md border p-2 font-mono text-xs"
							value={form.servers_yaml}
							onChange={(e) => setForm({ ...form, servers_yaml: e.target.value })}
							placeholder={SERVERS_EXAMPLE}
							spellCheck={false}
							data-testid="agent-mcp-servers"
						/>
					</Field>
					<ServerList data={data} />
					<div className="flex flex-wrap items-center gap-3">
						<Button disabled={!dirty || saving} onClick={() => void submit()} data-testid="agent-mcp-save">
							{saving ? t("tools.mcp.saving") : t("tools.mcp.save")}
						</Button>
						{data && data.servers.length > 0 && !dirty && <JobRunButton {...scanMCPJob(data.servers_file)} label={t("tools.mcp.scan")} variant="outline" />}
						{dirty && <span className="text-muted-foreground text-xs">{t("tools.mcp.saveFirst")}</span>}
					</div>
					{message && (
						<p className={message.ok ? "text-xs text-green-700" : "text-destructive text-xs"} data-testid="agent-mcp-message">
							{message.text}
						</p>
					)}
				</>
			)}
		</OpsSection>
	);
}

function ServerList({ data }: { data?: AgentMCPResponse }) {
	const { t } = useTranslation("agentops");
	if (!data || data.servers.length === 0) return null;
	return (
		<ul className="flex flex-wrap gap-2" data-testid="agent-mcp-server-list">
			{data.servers.map((s) => (
				<li key={s.id}>
					<Badge variant="secondary">
						{s.id}
						{s.image ? ` (${s.image.includes("@sha256:") ? t("tools.mcp.pinned") : t("tools.mcp.unpinned")})` : ""}
					</Badge>
				</li>
			))}
		</ul>
	);
}

export default ToolsTab;
