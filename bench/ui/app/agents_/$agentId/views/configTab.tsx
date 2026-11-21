import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useGetAgentConfigQuery, usePutAgentConfigMutation, useValidateAgentConfigMutation } from "@/lib/store/apis/agentBuilderApi";
import type { AgentConfigDoc, AgentConfigFields } from "@/lib/types/agentBuilder";
import { cn } from "@/lib/utils";
import { CheckCircle2, FileCode2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

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

function isConflict(error: unknown): boolean {
	return typeof error === "object" && error !== null && "status" in error && (error as { status: unknown }).status === 409;
}

/** Config tab: the agent's YAML as a form, or as raw YAML validated by the server, saved with If-Match. */
export function ConfigTab({ agentId }: { agentId?: string } & Record<string, unknown>) {
	const { t } = useTranslation("agentBuilder");
	const query = useGetAgentConfigQuery(agentId ?? "", { skip: !agentId });
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
	return <ConfigEditor key={query.data.sha256} doc={query.data} onReload={() => void query.refetch()} />;
}

function ConfigEditor({ doc, onReload }: { doc: AgentConfigDoc; onReload: () => void }) {
	const { t } = useTranslation("agentBuilder");
	const fileValid = doc.fields.adapter !== "";
	const [view, setView] = useState<"form" | "yaml">(fileValid ? "form" : "yaml");
	const [fields, setFields] = useState<AgentConfigFields>(doc.fields);
	const [envText, setEnvText] = useState(formatEnv(doc.fields.env));
	const [yamlText, setYamlText] = useState(doc.yaml);
	const [save, saveState] = usePutAgentConfigMutation();
	const [validate, validateState] = useValidateAgentConfigMutation();
	const [check, setCheck] = useState<{ ok: boolean; error?: string } | null>(null);

	const formFields = (): AgentConfigFields => ({ ...fields, env: parseEnv(envText) });
	const dirty = view === "yaml" ? yamlText !== doc.yaml : JSON.stringify(formFields()) !== JSON.stringify({ ...doc.fields, env: doc.fields.env ?? {} });

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
			setEnvText(formatEnv(res.data.fields.env));
			setView("form");
		} else if ("data" in res && res.data) {
			setCheck({ ok: false, error: res.data.error });
		}
	};

	const onValidate = async () => {
		const res = await validate(view === "yaml" ? { yaml: yamlText } : { yaml: doc.yaml, fields: formFields() });
		if ("data" in res && res.data) setCheck({ ok: res.data.ok, error: res.data.error });
	};

	const onSave = async () => {
		setCheck(null);
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
		await save(view === "yaml" ? { id: doc.id, yaml: yamlText, if_match: doc.sha256 } : { id: doc.id, fields: formFields(), if_match: doc.sha256 });
	};

	const set = <K extends keyof AgentConfigFields>(k: K, v: AgentConfigFields[K]) => setFields((cur) => ({ ...cur, [k]: v }));
	const num = (k: "wall_s" | "cpus" | "mem_gb" | "turns", label: string) => (
		<label className="flex flex-col gap-1 text-sm">
			<span className="font-medium">{label}</span>
			<Input type="number" min={0} step="any" value={fields[k] || ""} onChange={(e) => set(k, Number(e.target.value) || 0)} data-testid={`field-${k}`} />
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
			<div className="flex flex-wrap items-center gap-2">
				<div role="tablist" className="inline-flex rounded-md border p-0.5">
					{(["form", "yaml"] as const).map((v) => (
						<button
							key={v}
							type="button"
							role="tab"
							aria-selected={view === v}
							disabled={v === "form" && !fileValid && view === "yaml" && yamlText === doc.yaml}
							onClick={() => void switchTo(v)}
							className={cn("rounded px-3 py-1 text-sm", view === v ? "bg-primary text-primary-foreground" : "text-muted-foreground")}
							data-testid={`config-view-${v}`}
						>
							{t(`config.${v}`)}
						</button>
					))}
				</div>
				<span className="text-muted-foreground font-mono text-xs">{doc.path}</span>
			</div>
			{!fileValid && <p className="text-chart-warning-ink text-sm">{t("config.invalidFile")}</p>}

			{view === "form" ? (
				<div className="grid grid-cols-1 gap-4 md:grid-cols-2" data-testid="config-form">
					<label className="flex flex-col gap-1 text-sm">
						<span className="font-medium">{t("config.fields.adapter")}</span>
						<Input value={fields.adapter} disabled />
					</label>
					{text("model", t("config.fields.model"))}
					{text("provider", t("config.fields.provider"))}
					{fields.adapter === "command" && text("image", t("config.fields.image"))}
					{fields.adapter === "remote_http" && text("remote_url", t("config.fields.remote_url"))}
					{fields.adapter === "a2a" && text("a2a_card_url", t("config.fields.a2a_card_url"))}
					{num("wall_s", t("config.fields.wall_s"))}
					{num("cpus", t("config.fields.cpus"))}
					{num("mem_gb", t("config.fields.mem_gb"))}
					{num("turns", t("config.fields.turns"))}
					{fields.argv && fields.argv.length > 0 && (
						<label className="flex flex-col gap-1 text-sm md:col-span-2">
							<span className="font-medium">{t("config.fields.argv")}</span>
							<Input value={fields.argv.join(" ")} disabled className="font-mono" />
						</label>
					)}
					<label className="flex flex-col gap-1 text-sm md:col-span-2">
						<span className="font-medium">{t("config.fields.env")}</span>
						<textarea
							className="border-input bg-card min-h-28 rounded-md border px-3 py-2 font-mono text-xs"
							value={envText}
							onChange={(e) => setEnvText(e.target.value)}
							spellCheck={false}
							data-testid="field-env"
						/>
						<span className="text-muted-foreground text-xs">
							{t("config.fields.envHelp")} <code>{"{{run.key}}"}</code>
						</span>
					</label>
					<label className="flex flex-col gap-1 text-sm md:col-span-2">
						<span className="font-medium">{t("config.fields.description")}</span>
						<textarea
							className="border-input bg-card min-h-20 rounded-md border px-3 py-2 text-sm"
							value={fields.description ?? ""}
							onChange={(e) => set("description", e.target.value)}
							data-testid="field-description"
						/>
					</label>
				</div>
			) : (
				<textarea
					className="border-input bg-card min-h-[28rem] w-full rounded-md border px-3 py-2 font-mono text-xs"
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
				<p role={check.ok ? "status" : "alert"} className={cn("flex items-start gap-2 text-sm", check.ok ? "text-chart-success-ink" : "text-destructive")} data-testid="config-check">
					{check.ok && <CheckCircle2 className="mt-0.5 size-4" aria-hidden="true" />}
					{check.ok ? t("config.valid") : check.error}
				</p>
			)}
			{saveState.error && (
				<div role="alert" className="text-destructive flex flex-wrap items-center gap-2 text-sm" data-testid="config-save-error">
					{isConflict(saveState.error) ? t("config.conflict") : getBenchErrorMessage(saveState.error)}
					{isConflict(saveState.error) && (
						<Button size="sm" variant="outline" onClick={onReload}>
							{t("config.reload")}
						</Button>
					)}
				</div>
			)}
			<div className="flex flex-wrap gap-2">
				<Button onClick={() => void onSave()} disabled={!dirty || saveState.isLoading} data-testid="config-save">
					{saveState.isLoading ? t("config.saving") : t("config.save")}
				</Button>
				<Button variant="outline" onClick={() => void onValidate()} disabled={validateState.isLoading} data-testid="config-validate">
					{t("config.validate")}
				</Button>
				<Button
					variant="ghost"
					disabled={!dirty}
					onClick={() => {
						setFields(doc.fields);
						setEnvText(formatEnv(doc.fields.env));
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

export default ConfigTab;
