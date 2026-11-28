import { LabeledModelPicker } from "@/components/models/labeledModelPicker";
import { RoleGate, RoleNote } from "@/components/platform/accessNotes";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useGetAgentConfigQuery, usePutAgentConfigMutation, useValidateAgentConfigMutation } from "@/lib/store/apis/agentBuilderApi";
import { CAPABILITIES, useGetAgentVersionsQuery } from "@/lib/store/apis/evaluationsApi";
import { useLeaveGuard } from "@/lib/hooks/useLeaveGuard";
import type { AgentBehaviorFields, AgentConfigDoc, AgentConfigFields, AgentConfigValidateResponse } from "@/lib/types/agentBuilder";
import "@/lib/types/agentFirstI18n";
import "./uf2cI18n";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { CheckCircle2, ChevronRight, FileCode2, Wrench } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { PullServePanel } from "./pullServePanel";

/** "KEY=value" lines to an env map; blank lines and lines without "=" are skipped. */
export function parseEnv(text: string): Record<string, string> {
	const out: Record<string, string> = {};
	for (const line of text.split("\n")) {
		const i = line.indexOf("=");
		if (i <= 0) continue;
		const k = line.slice(0, i).trim();
		if (k) out[k] = line.slice(i + 1);
	}
	return out;
}

export function formatEnv(env: Record<string, string> | undefined): string {
	return Object.entries(env ?? {})
		.map(([k, v]) => `${k}=${v}`)
		.join("\n");
}

/** One item per line (or comma), trimmed, blanks dropped. */
export function parseList(text: string): string[] {
	return text
		.split(/[\n,]/)
		.map((x) => x.trim())
		.filter(Boolean);
}

export function formatList(items: readonly string[] | undefined, sep = "\n"): string {
	return (items ?? []).join(sep);
}

/** The text boxes of the form that are not plain fields. */
interface FormTexts {
	env: string;
	memory: string;
	fallbacks: string;
}

export function textsOf(fields: AgentConfigFields): FormTexts {
	return {
		env: formatEnv(fields.env),
		memory: formatList(fields.behavior?.memory_paths),
		fallbacks: formatList(fields.behavior?.fallbacks, ", "),
	};
}

/**
 * The fields the form sends: the edited fields with env, memory paths and fallbacks parsed from
 * their text boxes. An older server that sent no behavior gets none back, so it never sees keys it
 * does not know.
 */
export function buildFields(fields: AgentConfigFields, texts: FormTexts): AgentConfigFields {
	const out: AgentConfigFields = { ...fields, env: parseEnv(texts.env) };
	if (fields.behavior) {
		out.behavior = { ...fields.behavior, memory_paths: parseList(texts.memory), fallbacks: parseList(texts.fallbacks) };
	}
	return out;
}

/** The version a save would make: the newest saved version plus one, or null while unknown. */
export function saveNextVersion(versions: ReadonlyArray<{ version: number }> | undefined): number | null {
	if (!versions) return null;
	return versions.length === 0 ? 1 : Math.max(...versions.map((v) => v.version)) + 1;
}

/**
 * The cache key of an agent's settings save. The tab and its editor share the save's answer, so
 * the "Saved as version n" line survives the editor's remount on the new file.
 */
export function saveKey(agentId: string): string {
	return `agent-config-save:${agentId}`;
}

/** The Check settings line for a validate call: the server's verdict, or its error text. */
export function checkOf(res: { data?: Pick<AgentConfigValidateResponse, "ok" | "error">; error?: unknown }): {
	ok: boolean;
	error?: string;
} {
	if (res.data) return { ok: res.data.ok, error: res.data.error };
	return { ok: false, error: getBenchErrorMessage(res.error) };
}

function isConflict(error: unknown): boolean {
	return typeof error === "object" && error !== null && "status" in error && (error as { status: unknown }).status === 409;
}

/**
 * Setup tab: the agent's YAML as a form led by what makes two agents on the same model differ
 * (instructions, model, tools and MCP, memory, retries and limits, capabilities), with the container
 * details folded under "How it runs"; or as raw YAML validated by the server. Saved with If-Match.
 */
export function ConfigTab({ agentId, checkFailed = false }: { agentId?: string; checkFailed?: boolean } & Record<string, unknown>) {
	const { t } = useTranslation("agentBuilder");
	const query = useGetAgentConfigQuery(agentId ?? "", { skip: !agentId });
	const [, saved] = usePutAgentConfigMutation({ fixedCacheKey: saveKey(agentId ?? "") });
	if (!agentId) return null;
	if (query.isLoading) return <BlockSkeleton rows={6} testId="config-loading" />;
	if (query.isError || !query.data) {
		const status = (query.error as { status?: unknown } | undefined)?.status;
		if (status === 404) {
			return (
				<EmptyState
					icon={FileCode2}
					message={t("config.notFound", { id: agentId })}
					action={{ label: t("config.createOne"), to: "/agents/new" }}
					testId="config-not-found"
				/>
			);
		}
		return <LoadError title={t("config.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="config-error" />;
	}
	// The answer of the last save, shown while the file is still the one it saved.
	const last = saved.data && saved.data.id === agentId && saved.data.sha256 === query.data.sha256 ? saved.data : null;
	return (
		<div className="flex min-w-0 flex-col gap-4">
			{last && <SavedNotice doc={last} />}
			<ConfigEditor key={query.data.sha256} doc={query.data} onReload={() => void query.refetch()} checkFailed={checkFailed} />
		</div>
	);
}

/** "Saved as version n", or why the save kept the version. */
function SavedNotice({ doc }: { doc: AgentConfigDoc }) {
	const { t } = useTranslation("agentBuilder");
	const n = doc.version ?? 0;
	const text =
		n <= 0 ? t("config.savedNoVersion") : doc.version_created ? t("config.savedVersion", { n }) : t("config.savedSameVersion", { n });
	return (
		<p role="status" className="text-success-ink flex items-start gap-2 text-sm" data-testid="config-saved">
			<CheckCircle2 className="mt-0.5 size-4" aria-hidden="true" />
			{text}
		</p>
	);
}

/**
 * The folder of the agent's own code inside the project's agents area (the folder of its
 * build.dockerfile, e.g. "images/my-agent"), or "" for an agent without a build of its own.
 */
export function agentCodeDir(yaml: string): string {
	const m = /^[ \t]+dockerfile:[ \t]*["']?(agents\/[^\s"'#]+)["']?[ \t]*(#.*)?$/m.exec(yaml);
	if (!m) return "";
	const dir = m[1].slice("agents/".length).split("/").slice(0, -1).join("/");
	return dir.includes("..") ? "" : dir;
}

/** One titled group of the config form. */
function FormSection({ title, hint, children, testId }: { title: string; hint?: string; children: ReactNode; testId?: string }) {
	return (
		// A plain group under a divider, not a card in the panel (X-17, X-93); its title is an h2 (M2-53).
		<section
			className="grid grid-cols-1 gap-4 border-t pt-6 first:border-t-0 first:pt-0 md:grid-cols-[14rem_minmax(0,1fr)] md:gap-6"
			data-testid={testId}
		>
			<div className="flex flex-col gap-1">
				<h2 className="text-base font-semibold">{title}</h2>
				{hint && <p className="text-muted-foreground text-sm">{hint}</p>}
			</div>
			<div className="grid min-w-0 grid-cols-1 gap-4 md:grid-cols-2">{children}</div>
		</section>
	);
}

function ConfigEditor({ doc, onReload, checkFailed }: { doc: AgentConfigDoc; onReload: () => void; checkFailed: boolean }) {
	const { t } = useTranslation("agentBuilder");
	const fileValid = doc.fields.adapter !== "";
	const codeDir = agentCodeDir(doc.yaml);
	const [view, setView] = useState<"form" | "yaml">(fileValid ? "form" : "yaml");
	const { t: ta } = useTranslation("agentFirst");
	const { t: tu } = useTranslation("uf2c");
	const [fields, setFields] = useState<AgentConfigFields>(doc.fields);
	const [texts, setTexts] = useState<FormTexts>(() => textsOf(doc.fields));
	const [yamlText, setYamlText] = useState(doc.yaml);
	const [save, saveState] = usePutAgentConfigMutation({ fixedCacheKey: saveKey(doc.id) });
	// The save is shared with the tab; only a save this editor sent shows its error here.
	const [sent, setSent] = useState(false);
	const [validate, validateState] = useValidateAgentConfigMutation();
	const [check, setCheck] = useState<{ ok: boolean; error?: string } | null>(null);

	const formFields = (): AgentConfigFields => buildFields(fields, texts);
	const dirty =
		view === "yaml" ? yamlText !== doc.yaml : JSON.stringify(formFields()) !== JSON.stringify(buildFields(doc.fields, textsOf(doc.fields)));
	// Leaving the page or the tab with unsaved edits asks first (M2-48): browser prompt and in-app confirm.
	useLeaveGuard(dirty);
	// Save says which version it makes (M6-77): the newest saved version plus one.
	const versions = useGetAgentVersionsQuery(doc.id);
	const nextVersion = saveNextVersion(versions.data?.versions);
	const setText = (k: keyof FormTexts, v: string) => setTexts((cur) => ({ ...cur, [k]: v }));
	const behavior = fields.behavior;
	const setBehavior = <K extends keyof AgentBehaviorFields>(k: K, v: AgentBehaviorFields[K]) =>
		setFields((cur) => (cur.behavior ? { ...cur, behavior: { ...cur.behavior, [k]: v } } : cur));

	// Switching views carries the edit across: form to YAML renders the fields into the YAML server side.
	const switchTo = async (next: "form" | "yaml") => {
		if (next === view) return;
		setCheck(null);
		if (next === "yaml") {
			const res = await validate({ yaml: yamlText, fields: formFields() });
			if ("data" in res && res.data) setYamlText(res.data.yaml);
			setView("yaml");
			return;
		}
		const res = await validate({ yaml: yamlText });
		if ("data" in res && res.data?.ok && res.data.fields) {
			setFields(res.data.fields);
			setTexts(textsOf(res.data.fields));
			setView("form");
		} else if ("data" in res && res.data) {
			setCheck({ ok: false, error: res.data.error });
		}
	};

	const onValidate = async () => {
		const res = await validate(view === "yaml" ? { yaml: yamlText } : { yaml: doc.yaml, fields: formFields() });
		setCheck(checkOf(res));
	};

	const onSave = async () => {
		setCheck(null);
		setSent(true);
		if (view === "form" && yamlText !== doc.yaml) {
			// The YAML view was edited before switching back: keep those edits, apply the form on top.
			const res = await validate({ yaml: yamlText, fields: formFields() });
			if (!("data" in res) || !res.data?.ok) {
				setCheck({ ok: false, error: "data" in res ? res.data?.error : getBenchErrorMessage(res.error) });
				return;
			}
			await save({ id: doc.id, yaml: res.data.yaml, if_match: doc.sha256 });
			return;
		}
		await save(
			view === "yaml" ? { id: doc.id, yaml: yamlText, if_match: doc.sha256 } : { id: doc.id, fields: formFields(), if_match: doc.sha256 },
		);
	};

	const set = <K extends keyof AgentConfigFields>(k: K, v: AgentConfigFields[K]) => setFields((cur) => ({ ...cur, [k]: v }));
	const num = (k: "wall_s" | "cpus" | "mem_gb" | "turns", label: string) => (
		<label className="flex flex-col gap-1 text-sm">
			<span className="font-medium">{label}</span>
			<Input
				type="number"
				min={0}
				step={k === "turns" || k === "wall_s" ? 1 : "any"}
				inputMode={k === "turns" || k === "wall_s" ? "numeric" : "decimal"}
				placeholder={ta("setup.noLimit")}
				value={fields[k] || ""}
				onChange={(e) => {
					// Max steps and the time limit are whole numbers of at least 0; CPUs and memory may be fractions (p01-4).
					const n = Number(e.target.value) || 0;
					set(k, k === "turns" || k === "wall_s" ? Math.max(0, Math.trunc(n)) : Math.max(0, n));
				}}
				data-testid={`field-${k}`}
			/>
		</label>
	);
	const text = (k: "model" | "provider" | "image" | "remote_url" | "a2a_card_url", label: string) => (
		<label className="flex flex-col gap-1 text-sm">
			<span className="font-medium">{label}</span>
			<Input value={fields[k] ?? ""} onChange={(e) => set(k, e.target.value)} data-testid={`field-${k}`} />
		</label>
	);

	return (
		<div className="flex min-w-0 flex-col gap-4" data-testid="config-tab">
			{doc.fields.adapter === "pull" && <PullServePanel agentId={doc.id} yaml={doc.yaml} />}
			<div className="flex flex-wrap items-center gap-2">
				<div role="tablist" className="bg-muted inline-flex rounded-lg p-0.5">
					{(["form", "yaml"] as const).map((v) => (
						<button
							key={v}
							type="button"
							role="tab"
							aria-selected={view === v}
							disabled={v === "form" && !fileValid && view === "yaml" && yamlText === doc.yaml}
							onClick={() => void switchTo(v)}
							className={cn(
								"rounded-md px-3 py-1 text-sm transition",
								view === v ? "bg-background text-foreground shadow-sm" : "text-muted-foreground",
							)}
							data-testid={`config-view-${v}`}
						>
							{t(`config.${v}`)}
						</button>
					))}
				</div>
			</div>
			<p className="text-muted-foreground -mt-2 min-w-0 truncate text-xs" title={doc.path} data-testid="config-path">
				{tu("setup.savedIn")} <span className="font-mono">{doc.path}</span>
				{codeDir && (
					<>
						{" · "}
						<Link
							to="/settings"
							search={{ tab: "files", area: "agents", dir: codeDir } as never}
							className="text-primary hover:underline"
							data-testid="config-edit-code"
						>
							{tu("setup.editCode")}
						</Link>
					</>
				)}
			</p>
			{!fileValid && <p className="text-warning-ink text-sm">{t("config.invalidFile")}</p>}

			{view === "form" ? (
				<div className="flex flex-col gap-6" data-testid="config-form">
					<FormSection title={ta("setup.instructions")} testId="setup-instructions">
						<label className="flex flex-col gap-1 text-sm md:col-span-2">
							<span className="font-medium">{ta("setup.description")}</span>
							<textarea
								className="placeholder:text-neutral-500 border-input bg-card field-sizing-content min-h-32 rounded-md border px-3 py-2 text-sm"
								value={fields.description ?? ""}
								onChange={(e) => set("description", e.target.value)}
								data-testid="field-description"
							/>
						</label>
						{behavior && (
							<>
								<BehaviorText
									label={ta("setup.notesProjectFile")}
									value={behavior.notes_project_file}
									onChange={(v) => setBehavior("notes_project_file", v)}
									testId="field-notes_project_file"
								/>
								<BehaviorText
									label={ta("setup.notesUserFile")}
									value={behavior.notes_user_file}
									onChange={(v) => setBehavior("notes_user_file", v)}
									testId="field-notes_user_file"
								/>
								{behavior.notes_user_file && (
									<BehaviorText
										label={ta("setup.notesUserPath")}
										help={ta("setup.notesUserPathHelp")}
										value={behavior.notes_user_path}
										onChange={(v) => setBehavior("notes_user_path", v)}
										testId="field-notes_user_path"
									/>
								)}
							</>
						)}
					</FormSection>
					<FormSection title={ta("setup.model")} testId="setup-model">
						{/* The model list of /evaluate, not free text (M2-49); a model off the list stays as a custom id. */}
						<div className="md:col-span-2">
							<LabeledModelPicker
								label={t("config.fields.model")}
								value={fields.model ? { provider: fields.provider ?? "", model: fields.model } : null}
								onChange={(v) => setFields((cur) => ({ ...cur, model: v.model, provider: v.provider }))}
								testId="field-model"
							/>
						</div>
						{behavior && (
							<label className="flex flex-col gap-1 text-sm md:col-span-2">
								<span className="font-medium">{ta("setup.fallbacks")}</span>
								<Input
									value={texts.fallbacks}
									onChange={(e) => setText("fallbacks", e.target.value)}
									placeholder="gpt-4.1-mini, claude-haiku-4-5"
									data-testid="field-fallbacks"
								/>
								<span className="text-muted-foreground text-xs">{ta("setup.fallbacksHelp")}</span>
							</label>
						)}
					</FormSection>
					<FormSection title={ta("setup.tools")} testId="setup-tools">
						<div className="flex flex-wrap items-center gap-3 md:col-span-2">
							<Button asChild size="sm" variant="outline">
								<Link to="/agents/$agentId" params={{ agentId: doc.id }} search={{ tab: "tools" } as never} data-testid="setup-open-tools">
									<Wrench aria-hidden="true" />
									{ta("setup.openTools")}
								</Link>
							</Button>
						</div>
					</FormSection>
					{behavior && (
						<FormSection title={ta("setup.memory")} testId="setup-memory">
							<label className="flex flex-col gap-1 text-sm md:col-span-2">
								<span className="font-medium">{ta("setup.memoryPaths")}</span>
								<textarea
									className="placeholder:text-neutral-500 border-input bg-card min-h-20 rounded-md border px-3 py-2 font-mono text-xs"
									value={texts.memory}
									onChange={(e) => setText("memory", e.target.value)}
									placeholder="/root/.agent/memory"
									spellCheck={false}
									data-testid="field-memory_paths"
								/>
								<span className="text-muted-foreground text-xs">{ta("setup.memoryHelp")}</span>
							</label>
						</FormSection>
					)}
					<FormSection title={ta("setup.limits")} hint={ta("setup.limitsHint")} testId="setup-limits">
						{behavior && (
							<>
								<BehaviorNumber
									label={ta("setup.maxRetries")}
									value={behavior.max_retries}
									onChange={(v) => setBehavior("max_retries", v)}
									testId="field-max_retries"
									placeholder={ta("setup.noLimit")}
								/>
								<BehaviorNumber
									label={ta("setup.maxToolCalls")}
									value={behavior.max_tool_calls}
									onChange={(v) => setBehavior("max_tool_calls", v)}
									testId="field-max_tool_calls"
									placeholder={ta("setup.noLimit")}
								/>
								<BehaviorNumber
									label={ta("setup.providerRetries")}
									value={behavior.provider_retries}
									onChange={(v) => setBehavior("provider_retries", v)}
									testId="field-provider_retries"
									placeholder={ta("setup.noLimit")}
								/>
							</>
						)}
						{num("turns", t("config.fields.turns"))}
						{num("wall_s", t("config.fields.wall_s"))}
					</FormSection>
					{behavior && (
						<FormSection title={ta("setup.capabilities")} hint={ta("setup.capabilitiesHint")} testId="setup-capabilities">
							<fieldset className="flex flex-wrap gap-2 md:col-span-2">
								<legend className="sr-only">{ta("setup.capabilities")}</legend>
								{CAPABILITIES.map((cap) => {
									const on = behavior.capabilities.includes(cap);
									return (
										<label
											key={cap}
											className={cn(
												"inline-flex min-h-9 cursor-pointer items-center gap-2 rounded-md border px-2.5 py-1.5 text-sm",
												on ? "border-primary/50 bg-primary/5" : "text-muted-foreground",
											)}
										>
											<input
												type="checkbox"
												checked={on}
												onChange={(e) =>
													setBehavior(
														"capabilities",
														e.target.checked
															? CAPABILITIES.filter((c) => c === cap || behavior.capabilities.includes(c))
															: behavior.capabilities.filter((c) => c !== cap),
													)
												}
												data-testid={`field-capability-${cap}`}
											/>
											{ta(`setup.capability.${cap}`)}
										</label>
									);
								})}
							</fieldset>
							<p className="text-muted-foreground text-xs md:col-span-2">{ta("setup.capabilitiesHelp")}</p>
						</FormSection>
					)}
					{/* Open by default when the readiness check failed: these fields are what break runs (M2-51). */}
					<details className="group border-t" open={checkFailed || undefined} data-testid="setup-how-it-runs">
						<summary className="hover:bg-muted/40 flex cursor-pointer list-none items-center gap-2 rounded-md px-3 py-2 text-base font-semibold [&::-webkit-details-marker]:hidden">
							<ChevronRight className="text-muted-foreground size-4 transition-transform group-open:rotate-90" aria-hidden="true" />
							{ta("setup.howItRuns")}
						</summary>
						<div className="grid min-w-0 grid-cols-1 gap-4 px-3 py-2 md:grid-cols-2">
							<label className="flex flex-col gap-1 text-sm">
								<span className="font-medium">{t("config.fields.adapter")}</span>
								<Input value={fields.adapter} disabled />
							</label>
							{fields.adapter === "command" && text("image", t("config.fields.image"))}
							{fields.adapter === "remote_http" && text("remote_url", t("config.fields.remote_url"))}
							{fields.adapter === "a2a" && text("a2a_card_url", t("config.fields.a2a_card_url"))}
							{num("cpus", t("config.fields.cpus"))}
							{num("mem_gb", t("config.fields.mem_gb"))}
							{fields.argv && fields.argv.length > 0 && (
								<label className="flex flex-col gap-1 text-sm md:col-span-2">
									<span className="font-medium">{t("config.fields.argv")}</span>
									<Input value={fields.argv.join(" ")} disabled className="font-mono" />
								</label>
							)}
							<label className="flex flex-col gap-1 text-sm md:col-span-2">
								<span className="font-medium">{t("config.fields.env")}</span>
								<textarea
									className="placeholder:text-neutral-500 border-input bg-card min-h-28 rounded-md border px-3 py-2 font-mono text-xs"
									value={texts.env}
									onChange={(e) => setText("env", e.target.value)}
									spellCheck={false}
									data-testid="field-env"
								/>
								<span className="text-muted-foreground text-xs">
									{t("config.fields.envHelp")} <code>{"{{run.key}}"}</code>
								</span>
							</label>
						</div>
					</details>
				</div>
			) : (
				<textarea
					className="placeholder:text-neutral-500 border-input bg-card min-h-[28rem] w-full rounded-md border px-3 py-2 font-mono text-xs"
					value={yamlText}
					onChange={(e) => {
						setYamlText(e.target.value);
						setCheck(null);
					}}
					spellCheck={false}
					aria-label={t("config.yaml")}
					data-testid="config-yaml"
				/>
			)}

			{check && (
				<p
					role={check.ok ? "status" : "alert"}
					className={cn("flex items-start gap-2 text-sm", check.ok ? "text-success-ink" : "text-destructive")}
					data-testid="config-check"
				>
					{check.ok && <CheckCircle2 className="mt-0.5 size-4" aria-hidden="true" />}
					{check.ok ? t("config.checkValid") : check.error}
				</p>
			)}
			{sent && saveState.error && (
				<div role="alert" className="text-destructive flex flex-wrap items-center gap-2 text-sm" data-testid="config-save-error">
					{isConflict(saveState.error) ? t("config.conflict") : getBenchErrorMessage(saveState.error)}
					{isConflict(saveState.error) && (
						<Button size="sm" variant="outline" onClick={onReload}>
							{t("config.reload")}
						</Button>
					)}
				</div>
			)}
			<RoleNote min="member" />
			{/* Stays in view at the bottom while the long form scrolls, and says why Save is off (M2-48). */}
			<div className="bg-background/95 sticky bottom-0 z-10 flex flex-wrap items-center gap-2 border-t py-3 backdrop-blur" data-testid="config-actions">
				<span className={cn("mr-auto text-sm", dirty ? "font-medium" : "text-muted-foreground")} data-testid="config-dirty">
					{dirty ? tu("setup.unsaved") : tu("setup.noChanges")}
				</span>
				<RoleGate min="member" fallback={null}>
					<Button onClick={() => void onSave()} disabled={!dirty || saveState.isLoading} data-testid="config-save">
						{saveState.isLoading
							? t("config.saving")
							: nextVersion === null
								? t("config.save")
								: t("config.saveAsNext", { next: nextVersion, defaultValue: "Save as v{{next}}" })}
					</Button>
				</RoleGate>
				<Button variant="outline" onClick={() => void onValidate()} disabled={validateState.isLoading} data-testid="config-validate">
					{t("config.validate")}
				</Button>
				<Button
					variant="ghost"
					disabled={!dirty}
					onClick={() => {
						setFields(doc.fields);
						setTexts(textsOf(doc.fields));
						setYamlText(doc.yaml);
						setCheck(null);
					}}
				>
					{t("config.reset")}
				</Button>
			</div>
		</div>
	);
}

/** A text field of the behavior group, with one line of help. */
function BehaviorText({
	label,
	help,
	value,
	onChange,
	testId,
}: {
	label: string;
	help?: string;
	value: string;
	onChange: (v: string) => void;
	testId: string;
}) {
	return (
		<label className="flex flex-col gap-1 text-sm">
			<span className="font-medium">{label}</span>
			<Input value={value} onChange={(e) => onChange(e.target.value)} className="font-mono" data-testid={testId} />
			{help && <span className="text-muted-foreground text-xs">{help}</span>}
		</label>
	);
}

/** A whole-number field of the behavior group; empty or 0 means no limit. */
function BehaviorNumber({
	label,
	value,
	onChange,
	testId,
	placeholder,
}: {
	label: string;
	value: number;
	onChange: (v: number) => void;
	testId: string;
	placeholder?: string;
}) {
	return (
		<label className="flex flex-col gap-1 text-sm">
			<span className="font-medium">{label}</span>
			<Input
				type="number"
				min={0}
				step={1}
				inputMode="numeric"
				placeholder={placeholder}
				value={value || ""}
				onChange={(e) => onChange(Math.max(0, Math.trunc(Number(e.target.value) || 0)))}
				data-testid={testId}
			/>
		</label>
	);
}

export default ConfigTab;