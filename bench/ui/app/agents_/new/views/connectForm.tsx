import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { usePreviewEndpointMutation, useTestEndpointMutation } from "@/lib/store/apis/agentBuilderApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCreateSecretMutation, useListSecretsQuery } from "@/lib/store/apis/secretsApi";
import type { AgentFromTemplateRequest, AgentTemplate, EndpointTestResult, SentRequest, TemplateParam } from "@/lib/types/agentBuilder";
import { MAX_SECRET_BYTES, normalizeSecretName, secretNameProblem } from "@/lib/types/secrets";
import { cn } from "@/lib/utils";
import { ChevronRight, CircleCheck, CircleX, KeyRound, Loader2 } from "lucide-react";
import { useEffect, useId, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import "@/app/agents/views/w4m1I18n";
import "@/components/improvements/uf2bI18n";

/**
 * Connect your agent (lane X10, J3 and J4): the form fields of the bring-your-own templates by kind
 * (secret names, switches, numbers, a framework choice, KEY=VALUE lines), folded into Authentication,
 * Tools and Advanced, plus the "Test my endpoint" result. Lane Y10 adds the request and answer mapping
 * fold, fields shown only for some choices (show_when), a live preview of the request a sample task sends,
 * and an inline "New secret" form on every secret field.
 */

export type Params = Record<string, string>;

const GROUPS = ["mapping", "auth", "tools", "advanced"] as const;

/** Whether a field shows: each param named in show_when has one of the listed values. Hidden values are still sent. */
export function paramVisible(p: TemplateParam, params: Params): boolean {
	if (!p.show_when) return true;
	return Object.entries(p.show_when).every(([name, values]) => values.includes(params[name] ?? ""));
}

/** The text a select option shows. */
export function optionLabel(p: TemplateParam, o: string): string {
	if (p.option_labels?.[o]) return p.option_labels[o];
	return p.name === "framework" ? frameworkLabel(o) : o;
}

/** Caps a raw answer shown on the page. */
export const RAW_CAP = 8 * 1024;
export function capText(s: string, cap = RAW_CAP): string {
	return s.length > cap ? `${s.slice(0, cap)}\n...` : s;
}

/** The params a template starts with: every param's default. */
export function defaultParams(tpl: AgentTemplate): Params {
	const out: Params = {};
	for (const p of tpl.params) if (p.default !== undefined && p.default !== "") out[p.name] = p.default;
	return out;
}

/** Whether the model picker is hidden: the agent picks its own model (an A2A agent, or an HTTP endpoint whose model calls do not go through Heimdall). */
export function picksOwnModel(tpl: AgentTemplate, params: Params): boolean {
	if (tpl.adapter === "a2a" || tpl.adapter === "pull") return true;
	if (tpl.adapter === "remote_http") return params.model_traffic !== "true";
	return false;
}

/** Whether the template's agent can be tested by sending one sample task. */
export function testable(tpl: AgentTemplate): boolean {
	return tpl.adapter === "remote_http" || tpl.adapter === "openai_agent" || tpl.adapter === "a2a";
}

/** Required params still empty. "Wrap my existing app" also needs a repository or an image. */
export function missingParams(tpl: AgentTemplate, params: Params): string[] {
	const out = tpl.params.filter((p) => p.required && !(params[p.name] ?? "").trim()).map((p) => p.name);
	if (tpl.id === "wrap-app" && !(params.repo_url ?? "").trim() && !(params.image ?? "").trim()) out.push("repo_url");
	return out;
}

/**
 * What the form still needs, as the field labels a person sees. "Wrap my existing app" with neither a
 * repository nor an image gets its either-or rule as one entry (wrapEither) instead of a label.
 */
export function missingFieldLabels(tpl: AgentTemplate, params: Params): { labels: string[]; wrapEither: boolean } {
	const names = missingParams(tpl, params);
	const wrapEither = tpl.id === "wrap-app" && !(params.repo_url ?? "").trim() && !(params.image ?? "").trim();
	const labels = names
		.filter((n) => !(wrapEither && n === "repo_url" && !tpl.params.find((p) => p.name === n)?.required))
		.map((n) => tpl.params.find((p) => p.name === n)?.label ?? n);
	return { labels: [...new Set(labels)], wrapEither };
}

/** A pasted value that looks like a real key rather than a secret name: an sk- prefix, or longer than 30 characters. */
export function looksLikeKey(value: string): boolean {
	const v = value.trim();
	return /^sk-/i.test(v) || v.length > 30;
}

/**
 * A field that holds a secret NAME: a select of the org's secrets when the list loads, so a person
 * never pastes the key itself; otherwise a text box that warns when the value looks like a key.
 */
export function SecretNameField({
	value,
	onChange,
	testId,
	id,
	placeholder,
}: {
	value: string;
	onChange(v: string): void;
	testId: string;
	id?: string;
	placeholder?: string;
}) {
	const { t: tu } = useTranslation("uf2b");
	const list = useListSecretsQuery();
	const names = list.data?.secrets.map((x) => x.name) ?? [];
	if (names.length > 0) {
		const options = value && !names.includes(value) ? [value, ...names] : names;
		return (
			<select
				id={id}
				className="border-input bg-card h-9 w-full rounded-md border px-3 font-mono text-sm"
				value={value}
				onChange={(e) => onChange(e.target.value)}
				data-testid={testId}
			>
				<option value="">{tu("agent.secretPick")}</option>
				{options.map((n) => (
					<option key={n} value={n}>
						{n}
					</option>
				))}
			</select>
		);
	}
	return (
		<>
			<Input
				id={id}
				value={value}
				placeholder={placeholder}
				className="font-mono"
				autoComplete="off"
				onChange={(e) => onChange(e.target.value)}
				data-testid={testId}
			/>
			{looksLikeKey(value) && (
				<span role="alert" className="text-chart-warning-ink text-sm" data-testid={`${testId}-looks-like-key`}>
					{tu("agent.looksLikeKey")}
				</span>
			)}
		</>
	);
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

const FRAMEWORK_LABEL: Record<string, string> = {
	langgraph: "LangGraph",
	crewai: "CrewAI",
	"openai-agents": "OpenAI Agents SDK",
	custom: "Custom",
};

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
	const { t: tu } = useTranslation("uf2b");
	const id = `param-${p.name}`;
	if (p.kind === "bool") {
		return (
			<label className="flex items-start gap-2 text-sm">
				<input
					type="checkbox"
					className="mt-1"
					checked={value === "true"}
					onChange={(e) => onChange(e.target.checked ? "true" : "false")}
					data-testid={id}
				/>
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
			<select
				className="border-input bg-card h-9 rounded-md border px-3 text-sm"
				value={value}
				onChange={(e) => onChange(e.target.value)}
				data-testid={id}
			>
				{(p.options ?? []).map((o) => (
					<option key={o} value={o}>
						{optionLabel(p, o)}
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
	} else if (p.kind === "secret") {
		control = <SecretNameField value={value} onChange={onChange} testId={id} placeholder={p.placeholder} />;
	} else {
		control = (
			<Input
				value={value}
				placeholder={p.placeholder}
				required={p.required}
				inputMode={p.kind === "number" ? "numeric" : undefined}
				autoComplete="off"
				onChange={(e) => onChange(e.target.value)}
				data-testid={id}
			/>
		);
	}
	return (
		<div className="flex flex-col gap-1 text-sm">
			<label className="flex flex-col gap-1">
				<span className="font-medium">
					{p.label}
					{p.kind === "secret" && tu("agent.secretSuffix")}
					{p.required && <span className="text-muted-foreground font-normal"> {tu("agent.required")}</span>}
				</span>
				{control}
			</label>
			{(p.help || p.kind === "secret") && (
				<span className="text-muted-foreground text-xs">
					{p.help}
					{p.kind === "secret" && !p.help?.includes("Settings > Secrets") ? ` ${t("new.secretHelp")}` : ""}
				</span>
			)}
			{p.kind === "secret" && <NewSecretInline param={p.name} onSaved={onChange} />}
		</div>
	);
}

/**
 * "New secret" next to a secret field: stores a secret in Settings > Secrets without leaving the form
 * and fills the field with its name. The value is dropped from the page once saved. Admins only.
 */
export function NewSecretInline({ param, onSaved }: { param: string; onSaved(name: string): void }) {
	const { t } = useTranslation("agentBuilder");
	const { t: tm } = useTranslation("w4m1");
	const { t: ts } = useTranslation("secrets");
	const session = useSession();
	const formId = useId();
	const [open, setOpen] = useState(false);
	const [name, setName] = useState("");
	const [value, setValue] = useState("");
	const [touched, setTouched] = useState(false);
	const [saved, setSaved] = useState<string | null>(null);
	const [create, state] = useCreateSecretMutation();
	if (!canUse(session, "admin")) return null;

	const problem = secretNameProblem(name);
	const nameError = !touched
		? undefined
		: problem === "required"
			? ts("nameRequired")
			: problem === "format"
				? ts("nameFormat")
				: problem === "reserved"
					? ts("nameReserved")
					: undefined;
	const tooBig = new TextEncoder().encode(value).length > MAX_SECRET_BYTES;
	const valueError = !touched ? undefined : !value ? ts("valueRequired") : tooBig ? ts("valueTooLarge") : undefined;
	const close = () => {
		setOpen(false);
		setName("");
		setValue("");
		setTouched(false);
	};
	const save = async () => {
		setTouched(true);
		if (problem || !value || tooBig) return;
		try {
			const res = await create({ name, value }).unwrap();
			onSaved(res.name);
			setSaved(res.name);
			close();
		} catch {
			// state.error renders the failure; the value stays so the user can retry.
		}
	};
	// The page form must not submit when Enter is pressed in here.
	const onKey = (e: KeyboardEvent) => {
		if (e.key === "Enter") {
			e.preventDefault();
			void save();
		}
	};

	if (!open) {
		return (
			<p className="flex flex-wrap items-center gap-2 text-xs">
				<Button
					type="button"
					size="sm"
					variant="ghost"
					className="w-fit"
					onClick={() => {
						setOpen(true);
						setSaved(null);
					}}
					data-testid={`new-secret-${param}`}
				>
					<KeyRound className="size-3.5" aria-hidden="true" />
					{t("new.newSecret.button")}
				</Button>
				{saved && (
					<span className="text-chart-success-ink" data-testid={`new-secret-saved-${param}`} aria-live="polite">
						{t("new.newSecret.saved", { name: saved })}
					</span>
				)}
			</p>
		);
	}
	return (
		<div className="bg-muted/30 flex flex-col gap-2 rounded-md border p-3" data-testid={`new-secret-form-${param}`}>
			<div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
				<div className="flex min-w-0 flex-col gap-1">
					<label htmlFor={`${formId}-name`} className="text-xs font-medium">
						{ts("name")}
					</label>
					<Input
						id={`${formId}-name`}
						autoComplete="off"
						data-1p-ignore="true"
						data-lpignore="true"
						spellCheck={false}
						className="font-mono"
						value={name}
						onChange={(e) => setName(normalizeSecretName(e.target.value))}
						onKeyDown={onKey}
						aria-invalid={Boolean(nameError) || undefined}
						data-testid={`new-secret-name-${param}`}
					/>
					<span className="text-muted-foreground text-xs" data-testid={`new-secret-name-hint-${param}`}>
						{tm("secrets.nameHint")}
					</span>
					{nameError && <span className="text-destructive text-xs">{nameError}</span>}
				</div>
				<div className="flex min-w-0 flex-col gap-1">
					<label htmlFor={`${formId}-value`} className="text-xs font-medium">
						{ts("value")}
					</label>
					<Input
						id={`${formId}-value`}
						type="password"
						autoComplete="new-password"
						data-1p-ignore="true"
						data-lpignore="true"
						value={value}
						onChange={(e) => setValue(e.target.value)}
						onKeyDown={onKey}
						aria-invalid={Boolean(valueError) || undefined}
						data-testid={`new-secret-value-${param}`}
					/>
					{valueError && <span className="text-destructive text-xs">{valueError}</span>}
				</div>
			</div>
			{state.error ? (
				<p role="alert" className="text-destructive text-xs">
					{getBenchErrorMessage(state.error)}
				</p>
			) : null}
			<div className="flex items-center gap-2">
				<Button type="button" size="sm" disabled={state.isLoading} onClick={() => void save()} data-testid={`new-secret-save-${param}`}>
					{t("new.newSecret.save")}
				</Button>
				<Button type="button" size="sm" variant="ghost" onClick={close}>
					{ts("cancel")}
				</Button>
				<span className="text-muted-foreground text-xs">{t("new.newSecret.help")}</span>
			</div>
		</div>
	);
}

/** The template's params: ungrouped ones first, then a fold per group. */
export function ParamFields({ tpl, params, onChange }: { tpl: AgentTemplate; params: Params; onChange(p: Params): void }) {
	const { t } = useTranslation("agentBuilder");
	const set = (name: string, v: string) => onChange({ ...params, [name]: v });
	const detected = tpl.id === "wrap-app" ? detectFramework(params) : null;
	const field = (p: TemplateParam) =>
		!paramVisible(p, params) ? null : <ParamField key={p.name} p={p} value={params[p.name] ?? ""} onChange={(v) => set(p.name, v)} />;
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
				const ps = tpl.params.filter((p) => p.group === g && paramVisible(p, params));
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
		<div
			className={cn("flex flex-col gap-2 rounded-md border p-3 text-sm", ok ? "border-chart-success/40" : "border-destructive/40")}
			data-testid="endpoint-test-result"
			aria-live="polite"
		>
			<p className="flex items-center gap-2 font-medium">
				{ok ? (
					<CircleCheck className="text-chart-success-ink size-4" aria-hidden="true" />
				) : (
					<CircleX className="text-destructive size-4" aria-hidden="true" />
				)}
				{ok ? t(result.matched ? "new.test.ok" : "new.test.okNoMatch", { ms: result.latency_ms }) : t("new.test.failed")}
				{result.status ? <Badge variant="neutral">{t("new.test.status", { status: result.status })}</Badge> : null}
			</p>
			{result.error && <p className="text-destructive text-xs">{result.error}</p>}
			{result.answer && (
				<div className="flex flex-col gap-1">
					<span className="text-muted-foreground text-xs">{t("new.test.answer")}</span>
					<pre
						className="bg-muted max-h-40 overflow-auto rounded p-2 font-mono text-xs whitespace-pre-wrap"
						data-testid="endpoint-test-answer"
					>
						{result.answer}
					</pre>
				</div>
			)}
			{result.reads && (
				<p className="text-muted-foreground text-xs" data-testid="endpoint-test-reads">
					{t("new.endpointPreview.reads", { reads: result.reads })}
				</p>
			)}
			{result.requests && result.requests.length > 0 && (
				<details>
					<summary className="text-muted-foreground cursor-pointer text-xs select-none">
						{t("new.test.requests", { count: result.requests.length })}
					</summary>
					<div className="mt-1.5" data-testid="endpoint-test-requests">
						<SentRequests requests={result.requests} />
					</div>
				</details>
			)}
			{result.raw_response && (
				<details>
					<summary className="text-muted-foreground cursor-pointer text-xs select-none">{t("new.test.raw")}</summary>
					<pre
						className="bg-muted mt-1.5 max-h-60 overflow-auto rounded p-2 font-mono text-[11px] break-all whitespace-pre-wrap"
						data-testid="endpoint-test-raw"
					>
						{capText(result.raw_response)}
					</pre>
				</details>
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
				<Button
					type="button"
					size="sm"
					variant="outline"
					disabled={state.isLoading}
					onClick={() => void run({ agent: agentId })}
					data-testid="saved-endpoint-test-button"
				>
					{t("new.test.button")}
				</Button>
				<span className="text-muted-foreground text-xs">{t("new.test.help")}</span>
			</div>
			<EndpointTestPanel result={state.data} error={state.error} loading={state.isLoading} />
		</div>
	);
}

/** Requests as sent: method, URL, headers in send order, body. Secret values arrive masked. */
export function SentRequests({ requests }: { requests: SentRequest[] }) {
	const { t } = useTranslation("agentBuilder");
	return (
		<ol className="flex flex-col gap-2">
			{requests.map((r, i) => (
				<li key={i} className="bg-muted flex min-w-0 flex-col gap-1 rounded p-2 font-mono text-[11px]" data-testid="sent-request">
					<span className="flex min-w-0 flex-wrap items-center gap-2">
						{requests.length > 1 && r.step && (
							<Badge variant="neutral">{t(`new.endpointPreview.step.${r.step}`, { defaultValue: r.step })}</Badge>
						)}
						<span className="font-semibold">{r.method}</span>
						<span className="min-w-0 break-all">{r.url}</span>
					</span>
					{r.headers.length > 0 && (
						<ul className="text-muted-foreground flex flex-col">
							{r.headers.map(([k, v], j) => (
								<li key={j} className="break-all">
									{k}: {v}
								</li>
							))}
						</ul>
					)}
					{r.body && <pre className="max-h-40 overflow-auto break-all whitespace-pre-wrap">{capText(r.body)}</pre>}
				</li>
			))}
		</ol>
	);
}

/** How long the form waits after the last edit before asking for a fresh preview. */
export const PREVIEW_DELAY_MS = 500;

/** The live preview of the request a sample task sends, refreshed shortly after each edit. Nothing is sent. */
export function EndpointPreviewPanel({ body }: { body: AgentFromTemplateRequest | null }) {
	const [run, state] = usePreviewEndpointMutation();
	const key = body ? JSON.stringify(body) : "";
	useEffect(() => {
		if (!key) return;
		const timer = setTimeout(() => void run({ template: JSON.parse(key) as AgentFromTemplateRequest }), PREVIEW_DELAY_MS);
		return () => clearTimeout(timer);
	}, [key, run]);
	return <EndpointPreviewView data={state.data} error={state.error} loading={state.isLoading} />;
}

export function EndpointPreviewView({
	data,
	error,
	loading,
}: {
	data?: { ok: boolean; error?: string; requests: SentRequest[]; reads: string };
	error?: unknown;
	loading: boolean;
}) {
	const { t } = useTranslation("agentBuilder");
	return (
		<div className="flex min-w-0 flex-col gap-2 rounded-md border p-3 text-sm" data-testid="endpoint-preview" aria-live="polite">
			<p className="flex items-center gap-2 font-medium">
				{t("new.endpointPreview.title")}
				{loading && <Loader2 className="text-muted-foreground size-3.5 animate-spin" aria-hidden="true" />}
			</p>
			<span className="text-muted-foreground text-xs">{t("new.endpointPreview.help")}</span>
			{error ? (
				<p className="text-destructive text-xs" data-testid="endpoint-preview-error">
					{getBenchErrorMessage(error)}
				</p>
			) : data && !data.ok ? (
				<p className="text-muted-foreground text-xs" data-testid="endpoint-preview-error">
					{data.error || t("new.endpointPreview.empty")}
				</p>
			) : null}
			{data && data.requests.length > 0 && <SentRequests requests={data.requests} />}
			{data?.reads && (
				<p className="text-xs" data-testid="endpoint-preview-reads">
					{t("new.endpointPreview.reads", { reads: data.reads })}
				</p>
			)}
			{!data && !error && !loading && <p className="text-muted-foreground text-xs">{t("new.endpointPreview.empty")}</p>}
		</div>
	);
}

/**
 * "Your own code (pull mode)": the agent runs in the customer's process (heimdall.serve) and
 * takes each turn from Heimdall. The server has no template for it, so the form writes the YAML
 * itself through the config route.
 */
export const PULL_TEMPLATE: AgentTemplate = {
	id: "pull",
	name: "Your own code",
	category: "bring-your-own",
	description: "Your agent runs in your own process and asks Heimdall for work. Nothing has to reach your network.",
	adapter: "pull",
	providers: [],
	default_model: "",
	builds: false,
	params: [],
};

/** The gallery's templates with the pull option added once, after the bring-your-own ones. */
export function withPullTemplate(templates: readonly AgentTemplate[]): AgentTemplate[] {
	return templates.some((x) => x.adapter === "pull") ? [...templates] : [...templates, PULL_TEMPLATE];
}

export interface PullOptions {
	/** Heimdall drives the chat (conversation.driver bench). */
	benchDrives: boolean;
	history: "client" | "server";
	/** Coding tasks: send the repository, take a patch back (repo tarball). */
	repo: boolean;
}

export const DEFAULT_PULL_OPTIONS: PullOptions = { benchDrives: false, history: "client", repo: false };

/** The YAML of a new pull agent; pull.name is the agent id, which heimdall.serve registers under. */
export function pullAgentYAML(id: string, o: PullOptions, notes?: string): string {
	// Notes go in as comments: the YAML's notes key is the agent's instructions file, not free text.
	const lines = (notes ?? "").trim()
		? notes!
				.trim()
				.split("\n")
				.map((l) => `# ${l}`)
		: [];
	lines.push(`id: ${id}`, "adapter: pull", "image: bench/coding-core-base:1");
	lines.push("budgets:", "  wall_s: 900", "  cpus: 1", "  mem_gb: 2", "pull:", `  name: ${id}`);
	if (o.repo) lines.push("  repo: tarball");
	if (o.benchDrives) lines.push("  conversation:", "    driver: bench", `    history: ${o.history}`);
	return lines.join("\n") + "\n";
}

/** The pull agent's options: who drives the chat, and whether coding tasks send the repository. */
export function PullOptionsFields({ value, onChange }: { value: PullOptions; onChange(v: PullOptions): void }) {
	const { t } = useTranslation("agentBuilder");
	return (
		<div className="flex flex-col gap-4" data-testid="pull-fields">
			<p className="text-muted-foreground text-xs">{t("pull.form.intro")}</p>
			<label className="flex items-start gap-2 text-sm">
				<input
					type="checkbox"
					className="mt-1"
					checked={value.benchDrives}
					onChange={(e) => onChange({ ...value, benchDrives: e.target.checked })}
					data-testid="pull-bench-drives"
				/>
				<span className="flex flex-col gap-0.5">
					<span className="font-medium">{t("pull.form.benchDrives")}</span>
					<span className="text-muted-foreground text-xs">{t("pull.form.benchDrivesHelp")}</span>
				</span>
			</label>
			{value.benchDrives && (
				<label className="flex flex-col gap-1 text-sm">
					<span className="font-medium">{t("pull.form.history")}</span>
					<select
						className="border-input bg-card h-9 rounded-md border px-3 text-sm"
						value={value.history}
						onChange={(e) => onChange({ ...value, history: e.target.value === "server" ? "server" : "client" })}
						data-testid="pull-history"
					>
						<option value="client">{t("pull.form.historyClient")}</option>
						<option value="server">{t("pull.form.historyServer")}</option>
					</select>
				</label>
			)}
			<label className="flex items-start gap-2 text-sm">
				<input
					type="checkbox"
					className="mt-1"
					checked={value.repo}
					onChange={(e) => onChange({ ...value, repo: e.target.checked })}
					data-testid="pull-repo"
				/>
				<span className="flex flex-col gap-0.5">
					<span className="font-medium">{t("pull.form.repo")}</span>
					<span className="text-muted-foreground text-xs">{t("pull.form.repoHelp")}</span>
				</span>
			</label>
		</div>
	);
}