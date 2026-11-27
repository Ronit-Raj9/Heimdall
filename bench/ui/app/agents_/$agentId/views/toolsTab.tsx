import { NativeSelect } from "@/components/ui/select";
import { useConfirm } from "@/components/platform/confirmDialog";
import { RoleGate } from "@/components/platform/accessNotes";
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
	useScanAgentSkillMutation,
	useUploadAgentSkillMutation,
} from "@/lib/store/apis/agentopsApi";
import { MCP_FORMATS, scanMCPJob, type AgentMCPResponse, type AgentSkill, type SkillScanResult } from "@/lib/types/agentops";
import type { TFunction } from "i18next";
import { useEffect, useReducer, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "@tanstack/react-router";
import { FileArchive } from "lucide-react";
import "./uf2cI18n";
import "@/app/agents/views/ufreI18n";
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
			<GatewayLink />
		</div>
	);
}

/** The skill upload form: the picked zip, the skill name, whether the user typed the name, and the file input's key. */
export interface SkillForm {
	file: File | null;
	name: string;
	/** The user typed in the name box: picking another file keeps the typed name. */
	touched: boolean;
	/** Bumped after an upload so the browser's file input is emptied too. */
	inputKey: number;
}

export const initialSkillForm: SkillForm = { file: null, name: "", touched: false, inputKey: 0 };

export type SkillFormAction = { type: "pick"; file: File | null } | { type: "name"; name: string } | { type: "uploaded" };

/** The name follows each picked file until the user types one; an upload resets the form and the file input. */
export function skillFormReducer(s: SkillForm, a: SkillFormAction): SkillForm {
	switch (a.type) {
		case "pick":
			return { ...s, file: a.file, name: a.file && !s.touched ? skillNameFromFile(a.file.name) : s.name };
		case "name":
			return { ...s, name: a.name, touched: true };
		case "uploaded":
			return { ...initialSkillForm, inputKey: s.inputKey + 1 };
	}
}

type AgentOpsT = TFunction<"agentops">;

/** The message after an upload: installed, or not installed with the number of problems that block it. */
export function skillUploadMessage(t: AgentOpsT, res: SkillScanResult): { ok: boolean; text: string } {
	const { block, warn } = res.record.counts;
	if (!res.installed) return { ok: false, text: t("tools.skills.blockedUpload", { name: res.skill.name, count: block }) };
	const text = t("tools.skills.admitted", { name: res.skill.name });
	return { ok: true, text: warn > 0 ? `${text} ${t("tools.skills.admittedWarn", { warn })}` : text };
}

/** The verdict of a Scan press. */
export function skillScanMessage(t: AgentOpsT, res: SkillScanResult): { ok: boolean; text: string } {
	const { block, warn } = res.record.counts;
	return res.installed
		? { ok: true, text: t("tools.skills.scanAdmitted", { warn }) }
		: { ok: false, text: t("tools.skills.scanBlocked", { block, warn }) };
}

function SkillStatusBadge({ skill }: { skill: AgentSkill }) {
	const { t } = useTranslation("agentops");
	const testId = `agent-skill-status-${skill.name}`;
	if (skill.status === "installed")
		return (
			<Badge variant="outline" className="border-green-600 text-green-700" data-testid={testId}>
				{t("tools.skills.statusInstalled")}
			</Badge>
		);
	if (skill.status === "blocked")
		return (
			<Badge variant="destructive" data-testid={testId}>
				{t("tools.skills.statusBlocked")}
			</Badge>
		);
	if (skill.status === "not_scanned")
		return (
			<Badge variant="outline" className="border-amber-600 text-amber-700" data-testid={testId}>
				{t("tools.skills.statusNotScanned")}
			</Badge>
		);
	return null;
}

/** The findings of a skill's last scan that block or warn, under a Why disclosure. */
function SkillFindings({ skill }: { skill: AgentSkill }) {
	const { t } = useTranslation("agentops");
	const findings = skill.scan?.findings ?? [];
	if (findings.length === 0) return null;
	return (
		<details className="w-full text-xs" open={skill.status === "blocked"} data-testid={`agent-skill-findings-${skill.name}`}>
			<summary className="cursor-pointer">{t("tools.skills.why")}</summary>
			<ul className="mt-1 space-y-1">
				{findings.map((f, i) => (
					<li key={i} className={f.severity === "block" ? "text-destructive" : "text-amber-700"}>
						<span className="font-mono">{f.rule}</span> <span className="font-mono">{f.line ? `${f.file}:${f.line}` : f.file}</span>{" "}
						{f.detail}
					</li>
				))}
			</ul>
		</details>
	);
}

export function SkillsSection({ id, initial = initialSkillForm }: { id: string; initial?: SkillForm }) {
	const { t } = useTranslation("agentops");
	const { t: tu } = useTranslation("uf2c");
	const fileInput = useRef<HTMLInputElement>(null);
	const confirm = useConfirm();
	const { data, error, isLoading } = useGetAgentSkillsQuery(id);
	const [upload, { isLoading: uploading }] = useUploadAgentSkillMutation();
	const [scan] = useScanAgentSkillMutation();
	const [remove] = useDeleteAgentSkillMutation();
	const [form, dispatch] = useReducer(skillFormReducer, initial);
	const { file, name } = form;
	const [replace, setReplace] = useState(false);
	const [message, setMessage] = useState<{ ok: boolean; text: string } | null>(null);
	const [scanning, setScanning] = useState<string | null>(null);
	const [verdicts, setVerdicts] = useState<Record<string, { ok: boolean; text: string }>>({});
	const dir = data?.dir ?? `agents/${id}/skills`;

	const pick = (f: File | null) => {
		dispatch({ type: "pick", file: f });
		setMessage(null);
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
		else if (res.data) {
			setMessage(skillUploadMessage(t, res.data));
			dispatch({ type: "uploaded" });
		}
	};
	const runScan = async (skill: string) => {
		setScanning(skill);
		const res = await scan({ id, name: skill });
		setScanning(null);
		const verdict =
			"error" in res && res.error ? { ok: false, text: getBenchErrorMessage(res.error) } : res.data ? skillScanMessage(t, res.data) : null;
		if (verdict) setVerdicts((v) => ({ ...v, [skill]: verdict }));
	};
	const del = async (skill: string) => {
		if (!(await confirm(t("tools.skills.confirmDelete", { name: skill })))) return;
		const res = await remove({ id, name: skill });
		if ("error" in res && res.error) setMessage({ ok: false, text: getBenchErrorMessage(res.error) });
	};

	return (
		<OpsSection title={t("tools.skills.title")} hint={<span className="block max-w-prose">{t("tools.skills.hint", { dir })}</span>} testId="agent-skills-section">
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
							<SkillStatusBadge skill={s} />
							{!s.valid && <Badge variant="destructive">{t("tools.skills.noSkillMd")}</Badge>}
							<span className="ml-auto flex items-center gap-2">
								<RoleGate min="member" fallback={null}>
									<Button
										size="sm"
										variant="outline"
										disabled={scanning === s.name}
										onClick={() => void runScan(s.name)}
										data-testid={`agent-skill-scan-${s.name}`}
									>
										{scanning === s.name ? t("tools.skills.scanning") : t("tools.skills.scan")}
									</Button>
									<Button size="sm" variant="ghost" onClick={() => void del(s.name)} data-testid={`agent-skill-delete-${s.name}`}>
										{t("tools.skills.delete")}
									</Button>
								</RoleGate>
							</span>
							{verdicts[s.name] && (
								<p
									className={verdicts[s.name].ok ? "w-full text-xs text-green-700" : "text-destructive w-full text-xs"}
									data-testid={`agent-skill-verdict-${s.name}`}
								>
									{verdicts[s.name].text}
								</p>
							)}
							<SkillFindings skill={s} />
						</li>
					))}
				</ul>
			) : (
				<p className="text-muted-foreground text-xs" data-testid="agent-skills-empty">
					{t("tools.skills.empty")}
				</p>
			)}
			{data && data.skills.some((s) => s.status === "installed") && !data.installed && (
				<p className="text-xs text-amber-600">{t("tools.skills.notInstalled", { dir: data.dir })}</p>
			)}
			<RoleGate min="member">
				<div className="flex flex-wrap items-end gap-3">
					{/* A styled button opens the hidden file input and shows the picked name (M2-55). */}
					<Field label={t("tools.skills.zip")}>
						<input
							key={form.inputKey}
							ref={fileInput}
							type="file"
							accept=".zip,application/zip"
							className="sr-only"
							tabIndex={-1}
							onChange={(e) => pick(e.target.files?.[0] ?? null)}
							data-testid="agent-skill-file"
						/>
						<span className="flex min-w-0 items-center gap-2">
							<Button type="button" size="sm" variant="outline" onClick={() => fileInput.current?.click()} data-testid="agent-skill-choose">
								<FileArchive aria-hidden="true" />
								{tu("tools.chooseZip")}
							</Button>
							<span className="text-muted-foreground max-w-48 truncate text-xs" data-testid="agent-skill-file-name">
								{file ? file.name : tu("tools.noFile")}
							</span>
						</span>
					</Field>
					<Field label={t("tools.skills.name")}>
						<Input
							value={name}
							onChange={(e) => dispatch({ type: "name", name: e.target.value })}
							placeholder="code-review"
							data-testid="agent-skill-name"
						/>
					</Field>
					<label className="flex h-(--control-h) items-center gap-2 text-xs">
						<input type="checkbox" className="accent-primary size-4" checked={replace} onChange={(e) => setReplace(e.target.checked)} />
						{t("tools.skills.replace")}
					</label>
					<span className="flex h-(--control-h) items-center gap-2">
						<Button className="h-(--control-h)" disabled={!!problem || uploading} onClick={() => void submit()} data-testid="agent-skill-upload">
							{uploading ? t("tools.skills.uploading") : t("tools.skills.upload")}
						</Button>
						{!file && (
							<span className="text-muted-foreground text-xs" data-testid="agent-skill-upload-hint">
								{tu("tools.chooseFirst")}
							</span>
						)}
					</span>
				</div>
			</RoleGate>
			{file && problem && <p className="text-destructive text-xs">{problem}</p>}
			{file && !problem && (
				<p className="text-muted-foreground text-xs" data-testid="agent-skill-will-save">
					{t("tools.skills.willSave", { name, dir })}
				</p>
			)}
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
	const { t: tu } = useTranslation("uf2c");
	const { t: tr } = useTranslation("ufre");
	const { data, error, isLoading } = useGetAgentMCPQuery(id);
	const [save, { isLoading: saving }] = usePutAgentMCPMutation();
	const [form, setForm] = useState(() => ({
		format: data?.format ?? "",
		mount_path: data?.mount_path ?? "",
		servers_yaml: data?.servers_yaml ?? "",
	}));
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
			hint={
				<span className="block max-w-prose">
					{/* The MCP server test page tries one server on its own (M5-52). */}
					<span data-testid="agent-mcp-test">
						{tu("tools.mcpTestIntro")}{" "}
						<Link to="/mcp-test" className="text-primary underline-offset-4 hover:underline" data-testid="agent-mcp-test-link">
							{tu("tools.mcpTestLink")}
						</Link>
						.
					</span>
				</span>
			}
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
							<NativeSelect
								wrapperClassName="w-full"
								value={form.format}
								onChange={(e) => setForm({ ...form, format: e.target.value })}
								data-testid="agent-mcp-format"
							>
								{MCP_FORMATS.map((f) => (
									<option key={f} value={f}>
										{mcpFormatLabel(f, t("tools.mcp.adapterDefault"), (k) => tr(`tools.mcpFormat.${k}`))}
									</option>
								))}
							</NativeSelect>
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
							className="placeholder:text-neutral-500 border-input bg-background min-h-40 rounded-md border p-2 font-mono text-xs"
							value={form.servers_yaml}
							onChange={(e) => setForm({ ...form, servers_yaml: e.target.value })}
							placeholder={tu("tools.noServers")}
							spellCheck={false}
							data-testid="agent-mcp-servers"
						/>
					</Field>
					{/* The example is inserted on demand, never shown as if it were real config (M2-56). */}
					{form.servers_yaml.trim() === "" && (
						<div>
							<Button
								type="button"
								size="sm"
								variant="outline"
								onClick={() => setForm({ ...form, servers_yaml: SERVERS_EXAMPLE })}
								data-testid="agent-mcp-insert-example"
							>
								{tu("tools.insertExample")}
							</Button>
						</div>
					)}
					<ServerList data={data} />
					<div className="flex flex-wrap items-center gap-3">
						<RoleGate min="member">
							<Button disabled={!dirty || saving} onClick={() => void submit()} data-testid="agent-mcp-save">
								{saving ? t("tools.mcp.saving") : t("tools.mcp.save")}
							</Button>
						</RoleGate>
						{data && data.servers.length > 0 && !dirty && (
							<JobRunButton {...scanMCPJob(data.servers_file)} label={t("tools.mcp.scan")} variant="outline" />
						)}
						{dirty && <span className="text-muted-foreground text-xs">{t("tools.mcp.saveFirst")}</span>}
						{!dirty && (
							<span className="text-muted-foreground text-xs" data-testid="agent-mcp-nothing-changed">
								{tu("tools.nothingChanged")}
							</span>
						)}
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

/** The way to the gateway report card, which shows what the agent's tool calls did (X-91). */
function GatewayLink() {
	const { t } = useTranslation("uf2c");
	return (
		<p className="text-muted-foreground max-w-prose text-sm" data-testid="agent-tools-gateway">
			{t("tools.gatewayIntro")}{" "}
			<Link to="/gateway" className="text-primary underline-offset-4 hover:underline" data-testid="agent-tools-gateway-link">
				{t("tools.gatewayLink")}
			</Link>
		</p>
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

/** The label of an MCP config format (Q-291): what is written where, never the raw id; "" is the adapter's default. */
export function mcpFormatLabel(format: string, adapterDefault: string, label: (key: Exclude<(typeof MCP_FORMATS)[number], "">) => string): string {
	if (!format) return adapterDefault;
	return (MCP_FORMATS as readonly string[]).includes(format) ? label(format as Exclude<(typeof MCP_FORMATS)[number], "">) : format;
}
