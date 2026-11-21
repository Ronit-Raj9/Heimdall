import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useTestEndpointMutation } from "@/lib/store/apis/agentBuilderApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import type { AgentTemplate, EndpointTestResult, TemplateParam } from "@/lib/types/agentBuilder";
import { cn } from "@/lib/utils";
import { ChevronRight, CircleCheck, CircleX, Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * Connect your agent (lane X10, J3 and J4): the form fields of the bring-your-own templates by kind
 * (secret names, switches, numbers, a framework choice, KEY=VALUE lines), folded into Authentication,
 * Tools and Advanced, plus the "Test my endpoint" result.
 */

export type Params = Record<string, string>;

const GROUPS = ["auth", "tools", "advanced"] as const;

/** The params a template starts with: every param's default. */
export function defaultParams(tpl: AgentTemplate): Params {
	const out: Params = {};
	for (const p of tpl.params) if (p.default !== undefined && p.default !== "") out[p.name] = p.default;
	return out;
}

/** Whether the model picker is hidden: the agent picks its own model (an A2A agent, or an HTTP endpoint whose model calls do not go through Heimdall). */
export function picksOwnModel(tpl: AgentTemplate, params: Params): boolean {
	if (tpl.adapter === "a2a") return true;
	if (tpl.adapter === "remote_http") return params.model_traffic !== "true";
	return false;
}

/** Whether the template's agent can be tested by sending one sample task. */
export function testable(tpl: AgentTemplate): boolean {
	return tpl.adapter === "remote_http" || tpl.adapter === "openai_agent";
}

/** Required params still empty. "Wrap my existing app" also needs a repository or an image. */
export function missingParams(tpl: AgentTemplate, params: Params): string[] {
	const out = tpl.params.filter((p) => p.required && !(params[p.name] ?? "").trim()).map((p) => p.name);
	if (tpl.id === "wrap-app" && !(params.repo_url ?? "").trim() && !(params.image ?? "").trim()) out.push("repo_url");
	return out;
}

const FRAMEWORK_SIGNS: [string, RegExp][] = [
	["langgraph", /langgraph|langchain/i],
	["crewai", /crew[-_]?ai|\bcrew\b/i],
	["openai-agents", /openai[-_]?agents|agents[-_]?sdk/i],
];

/** A framework guess from the repository, image and start command, or null. */
export function detectFramework(params: Params): string | null {
	const text = [params.repo_url, params.image, params.command].filter(Boolean).join(" ");
	for (const [id, re] of FRAMEWORK_SIGNS) if (re.test(text)) return id;
	return null;
}

const FRAMEWORK_LABEL: Record<string, string> = { langgraph: "LangGraph", crewai: "CrewAI", "openai-agents": "OpenAI Agents SDK", custom: "Custom" };

const HINT_KEYS = {
	langgraph: "new.frameworkHint.langgraph",
	crewai: "new.frameworkHint.crewai",
	"openai-agents": "new.frameworkHint.openai-agents",
	custom: "new.frameworkHint.custom",
} as const;

/** The i18n key of a framework's hint; unknown ones get the custom hint. */
export function frameworkHintKey(id: string | undefined) {
	return HINT_KEYS[(id ?? "custom") as keyof typeof HINT_KEYS] ?? HINT_KEYS.custom;
}

export function frameworkLabel(id: string): string {
	return FRAMEWORK_LABEL[id] ?? id;
}

function ParamField({ p, value, onChange }: { p: TemplateParam; value: string; onChange(v: string): void }) {
	const { t } = useTranslation("agentBuilder");
	const id = `param-${p.name}`;
	if (p.kind === "bool") {
		return (
			<label className="flex items-start gap-2 text-sm">
				<input type="checkbox" className="mt-1" checked={value === "true"} onChange={(e) => onChange(e.target.checked ? "true" : "false")} data-testid={id} />
				<span className="flex flex-col gap-0.5">
					<span className="font-medium">{p.label}</span>
					{p.help && <span className="text-muted-foreground text-xs">{p.help}</span>}
				</span>
			</label>
		);
	}
	let control;
	if (p.kind === "select") {
		control = (
			<select className="border-input bg-card h-9 rounded-md border px-3 text-sm" value={value} onChange={(e) => onChange(e.target.value)} data-testid={id}>
				{(p.options ?? []).map((o) => (
					<option key={o} value={o}>
						{p.name === "framework" ? frameworkLabel(o) : o}
					</option>
				))}
			</select>
		);
	} else if (p.kind === "multiline") {
		control = (
			<textarea
				className="border-input bg-card min-h-16 rounded-md border px-3 py-2 font-mono text-xs"
				value={value}
				placeholder={p.placeholder}
				onChange={(e) => onChange(e.target.value)}
				data-testid={id}
			/>
		);
	} else {
		control = (
			<Input
				value={value}
				placeholder={p.placeholder}
				required={p.required}
				inputMode={p.kind === "number" ? "numeric" : undefined}
				className={p.kind === "secret" ? "font-mono" : undefined}
				autoComplete="off"
				onChange={(e) => onChange(e.target.value)}
				data-testid={id}
			/>
		);
	}
	return (
		<label className="flex flex-col gap-1 text-sm">
			<span className="font-medium">
				{p.label}
				{p.required && <span className="text-muted-foreground"> *</span>}
			</span>
			{control}
			{(p.help || p.kind === "secret") && (
				<span className="text-muted-foreground text-xs">
					{p.help}
					{p.kind === "secret" && !p.help?.includes("Settings > Secrets") ? ` ${t("new.secretHelp")}` : ""}
				</span>
			)}
		</label>
	);
}

/** The template's params: ungrouped ones first, then a fold per group. */
export function ParamFields({ tpl, params, onChange }: { tpl: AgentTemplate; params: Params; onChange(p: Params): void }) {
	const { t } = useTranslation("agentBuilder");
	const set = (name: string, v: string) => onChange({ ...params, [name]: v });
	const detected = tpl.id === "wrap-app" ? detectFramework(params) : null;
	const field = (p: TemplateParam) => <ParamField key={p.name} p={p} value={params[p.name] ?? ""} onChange={(v) => set(p.name, v)} />;
	return (
		<div className="flex flex-col gap-4" data-testid="connect-fields">
			{tpl.params.filter((p) => !p.group).map(field)}
			{detected && detected !== params.framework && (
				<p className="flex items-center gap-2 text-xs" data-testid="framework-detected">
					<Badge variant="info">{t("new.detected", { framework: frameworkLabel(detected) })}</Badge>
					<Button type="button" size="sm" variant="ghost" onClick={() => set("framework", detected)}>
						{t("new.useDetected")}
					</Button>
				</p>
			)}
			{tpl.id === "wrap-app" && (
				<p className="bg-muted/50 rounded-md p-2 text-xs" data-testid="framework-hint">
					{t(frameworkHintKey(params.framework))}
				</p>
			)}
			{GROUPS.map((g) => {
				const ps = tpl.params.filter((p) => p.group === g);
				if (!ps.length) return null;
				const open = g === "auth" && ps.some((p) => (params[p.name] ?? "") !== "");
				return (
					<details key={g} className="group/fold rounded-md border px-3 py-2" open={open || undefined} data-testid={`param-group-${g}`}>
						<summary className="flex cursor-pointer list-none items-center gap-1 text-sm font-medium select-none">
							<ChevronRight className="size-3.5 transition-transform group-open/fold:rotate-90" aria-hidden="true" />
							{t(`new.groups.${g}`)}
						</summary>
						<div className="mt-3 flex flex-col gap-4">{ps.map(field)}</div>
					</details>
				);
			})}
		</div>
	);
}

/** The "Test my endpoint" result: answer, latency, notes and the trace. */
export function EndpointTestPanel({ result, error, loading }: { result?: EndpointTestResult; error?: unknown; loading: boolean }) {
	const { t } = useTranslation("agentBuilder");
	if (loading) {
		return (
			<p className="text-muted-foreground flex items-center gap-2 text-sm" data-testid="endpoint-test-running" aria-live="polite">
				<Loader2 className="size-4 animate-spin" aria-hidden="true" />
				{t("new.test.running")}
			</p>
		);
	}
	if (error) {
		return (
			<p role="alert" className="text-destructive text-sm" data-testid="endpoint-test-error">
				{getBenchErrorMessage(error)}
			</p>
		);
	}
	if (!result) return null;
	const ok = result.ok;
	return (
		<div className={cn("flex flex-col gap-2 rounded-md border p-3 text-sm", ok ? "border-chart-success/40" : "border-destructive/40")} data-testid="endpoint-test-result" aria-live="polite">
			<p className="flex items-center gap-2 font-medium">
				{ok ? <CircleCheck className="text-chart-success-ink size-4" aria-hidden="true" /> : <CircleX className="text-destructive size-4" aria-hidden="true" />}
				{ok ? t(result.matched ? "new.test.ok" : "new.test.okNoMatch", { ms: result.latency_ms }) : t("new.test.failed")}
				{result.status ? <Badge variant="neutral">{t("new.test.status", { status: result.status })}</Badge> : null}
			</p>
			{result.error && <p className="text-destructive text-xs">{result.error}</p>}
			{result.answer && (
				<div className="flex flex-col gap-1">
					<span className="text-muted-foreground text-xs">{t("new.test.answer")}</span>
					<pre className="bg-muted max-h-40 overflow-auto rounded p-2 font-mono text-xs whitespace-pre-wrap" data-testid="endpoint-test-answer">
						{result.answer}
					</pre>
				</div>
			)}
			{result.notes.map((n) => (
				<p key={n} className="text-muted-foreground text-xs">
					{n}
				</p>
			))}
			{result.trace.length > 0 && (
				<details>
					<summary className="text-muted-foreground cursor-pointer text-xs select-none">{t("new.test.trace")}</summary>
					<ol className="mt-1.5 flex flex-col gap-1 font-mono text-[11px]" data-testid="endpoint-test-trace">
						{result.trace.map((s, i) => (
							<li key={i} className="flex gap-2">
								<span className="text-muted-foreground w-14 shrink-0 text-right tabular-nums">{s.at_ms} ms</span>
								<span className={cn("w-16 shrink-0", s.kind === "error" && "text-destructive")}>{s.kind}</span>
								<span className="min-w-0 break-all">{s.detail}</span>
							</li>
						))}
					</ol>
				</details>
			)}
		</div>
	);
}

/** "Test my endpoint" for a saved HTTP or OpenAI-compatible agent (agent page). */
export function SavedEndpointTest({ agentId }: { agentId: string }) {
	const { t } = useTranslation("agentBuilder");
	const [run, state] = useTestEndpointMutation();
	return (
		<div className="flex min-w-0 flex-col gap-2" data-testid="saved-endpoint-test">
			<div className="flex flex-wrap items-center gap-3">
				<Button type="button" size="sm" variant="outline" disabled={state.isLoading} onClick={() => void run({ agent: agentId })} data-testid="saved-endpoint-test-button">
					{t("new.test.button")}
				</Button>
				<span className="text-muted-foreground text-xs">{t("new.test.help")}</span>
			</div>
			<EndpointTestPanel result={state.data} error={state.error} loading={state.isLoading} />
		</div>
	);
}
