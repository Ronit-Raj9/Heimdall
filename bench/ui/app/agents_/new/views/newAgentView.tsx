import { agentNameProblem } from "@/components/agents/agentName";
import { pageWidthClass } from "@/components/shell/pageWidth";
import { GroupedModelPicker } from "@/components/models/groupedModelPicker";
import type { ModelRef, ProviderId } from "@/components/models/modelPicker";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState, PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { BIFROST_PROVIDERS } from "@/components/models/providerCatalog";
import { keyedProviders } from "@/app/experiments_/new/views/matrixBuilderSchema";
import { useGetModelsQuery } from "@/lib/store/apis/modelsApi";
import "@/components/improvements/uf2bI18n";
import {
	useCreateAgentFromTemplateMutation,
	useGetAgentTemplatesQuery,
	usePutAgentConfigMutation,
	useTestEndpointMutation,
} from "@/lib/store/apis/agentBuilderApi";
import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import type { AgentTemplate, TemplateCategory } from "@/lib/types/agentBuilder";
import { cn } from "@/lib/utils";
import { Link, useNavigate } from "@tanstack/react-router";
import {
	Bot,
	Check,
	ChevronRight,
	CircleCheck,
	Code2,
	CloudDownload,
	Globe,
	Hammer,
	Layers,
	Mic,
	Network,
	Package,
	Phone,
	Plug,
	Radio,
	SquareTerminal,
	type LucideIcon,
} from "lucide-react";
import { useAdapterLabel } from "../../../agents/views/agentDisplay";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useMemo, useRef, useState } from "react";
import { flushSync } from "react-dom";
import { useFormDraft } from "@/lib/hooks/useFormDraft";
import { useLeaveGuard } from "@/lib/hooks/useLeaveGuard";
import {
	DEFAULT_PULL_OPTIONS,
	defaultParams,
	EndpointPreviewPanel,
	EndpointTestPanel,
	missingFieldLabels,
	missingParams,
	ParamFields,
	picksOwnModel,
	pullAgentYAML,
	PullOptionsFields,
	testable,
	withPullTemplate,
	type PullOptions,
} from "./connectForm";
import { PlatformImportCard } from "./platformImport";
import { useTranslation } from "react-i18next";

// "Bring your own" first: the gallery is for the user's own agent; the others are examples.
export const CATEGORIES: TemplateCategory[] = ["bring-your-own", "starter", "framework", "cli"];
const CATEGORY_ICON: Record<TemplateCategory, LucideIcon> = {
	cli: SquareTerminal,
	framework: Layers,
	starter: Code2,
	"bring-your-own": Plug,
};
const ID_RE = /^[a-z0-9][a-z0-9._-]{0,62}$/;

/** The templates most people start from, shown first in their own row (when the server lists them). */
export const MOST_USED = ["http-url", "command", "docker-image", "claude-code"] as const;

/** The "Most used" row, then each category's other templates; a template shows in one place only. */
export function templateGroups(templates: readonly AgentTemplate[]): {
	mostUsed: AgentTemplate[];
	groups: { cat: TemplateCategory; items: AgentTemplate[] }[];
} {
	const mostUsed = MOST_USED.map((id) => templates.find((x) => x.id === id)).filter((x): x is AgentTemplate => Boolean(x));
	const shown = new Set(mostUsed.map((x) => x.id));
	const groups = CATEGORIES.map((cat) => ({ cat, items: templates.filter((x) => x.category === cat && !shown.has(x.id)) })).filter(
		(g) => g.items.length > 0,
	);
	return { mostUsed, groups };
}

/** One icon per kind of agent, so template rows differ at a glance (avatars are for real agents only). */
export function templateIcon(tpl: Pick<AgentTemplate, "id" | "adapter" | "category">): LucideIcon {
	if (tpl.id === "docker-image") return Package;
	if (/phone|vapi|retell|twilio/i.test(tpl.id)) return Phone;
	if (/voice|livekit|pipecat/i.test(tpl.id + tpl.adapter)) return Mic;
	switch (tpl.adapter) {
		case "remote_http":
		case "openai_agent":
			return Globe;
		case "a2a":
			return Network;
		case "pull":
			return Code2;
	}
	if (tpl.category === "cli" || tpl.id === "command") return SquareTerminal;
	return tpl.category === "framework" ? Layers : Code2;
}

/**
 * The agent id a typed name is saved as: lower case, spaces and other characters as dashes,
 * no leading or trailing dash, at most 63 characters. Typing stays as typed; this is what is saved.
 */
export function agentIdFromName(name: string): string {
	return name
		.trim()
		.toLowerCase()
		.replace(/[^a-z0-9._-]+/g, "-")
		.replace(/^[-._]+/, "")
		.slice(0, 63)
		.replace(/-+$/, "");
}

/**
 * The default name for template t: the first of "my-<template>", "my-<template>-2",
 * "my-<template>-3", ... that no existing agent uses.
 */
export function defaultAgentName(t: AgentTemplate, taken: ReadonlySet<string> = new Set()): string {
	const base = `my-${t.id}`;
	if (!taken.has(base)) return base;
	for (let n = 2; ; n++) {
		if (!taken.has(`${base}-${n}`)) return `${base}-${n}`;
	}
}

/**
 * Why name cannot be used for a new agent, or "" when it can: empty, too long (with the count), a bad first
 * character, any other bad character, then a name another agent already uses.
 */
export function nameProblem(name: string, taken: ReadonlySet<string>): string {
	return agentNameProblem(name, taken);
}

/**
 * New agent wizard (/agents/new?template=): pick a template (one click), the name and model are
 * pre-filled, Create writes agents/<name>.yaml through the project store and opens the agent.
 */
/** What the new agent form keeps as a session draft: the picked template and what was typed for it. */
export interface NewAgentDraft {
	template: string;
	typedName: string | null;
	notes: string;
	params: Record<string, string>;
}

export const EMPTY_AGENT_DRAFT: NewAgentDraft = { template: "", typedName: null, notes: "", params: {} };

const SECRETISH = /token|password|secret|api_?key/i;

/** The params safe to keep in the draft: never a secret field, nor one whose name says key or token. */
export function storableParams(tpl: AgentTemplate, params: Record<string, string>): Record<string, string> {
	const secret = new Set(tpl.params.filter((p) => p.kind === "secret" || SECRETISH.test(p.name)).map((p) => p.name));
	const out: Record<string, string> = {};
	for (const [k, v] of Object.entries(params)) if (!secret.has(k) && !SECRETISH.test(k)) out[k] = v;
	return out;
}

/** True once the person typed a name or notes, or changed a field from its default. */
export function agentFormDirty(tpl: AgentTemplate | null, typedName: string | null, notes: string, params: Record<string, string>): boolean {
	if (!tpl) return false;
	if (typedName !== null || notes !== "") return true;
	const base = defaultParams(tpl);
	const keys = new Set([...Object.keys(base), ...Object.keys(params)]);
	for (const k of keys) if ((base[k] ?? "") !== (params[k] ?? "")) return true;
	return false;
}

export default function NewAgentView() {
	const { t } = useTranslation("agentBuilder");
	const { t: tu } = useTranslation("agentsUx");
	const { t: tx } = useTranslation("uf2b");
	const adapterLabel = useAdapterLabel();
	const canWrite = canUse(useSession(), "member");
	const navigate = useNavigate();
	const query = useGetAgentTemplatesQuery();
	const [templateId, setTemplateId] = useQueryState("template", parseAsString.withOptions({ history: "replace" }));
	// ?return=/evaluate: the /evaluate wizard sent the user here and gets the new agent back.
	const [returnTo] = useQueryState("return", parseAsString);
	const templates = useMemo(() => withPullTemplate(query.data?.templates ?? []), [query.data]);
	const tpl = templates.find((x) => x.id === templateId) ?? null;
	// The template under the pointer or focus: its description shows in the side panel.
	const [hovered, setHovered] = useState<AgentTemplate | null>(null);
	const formRef = useRef<HTMLDivElement>(null);
	const models = useGetModelsQuery();
	const keyed = useMemo(() => keyedProviders(models.data?.keys ?? []), [models.data]);

	// null: the name was not edited, so it follows the first free default.
	const [typedName, setTypedName] = useState<string | null>(null);
	const [model, setModel] = useState<ModelRef | null>(null);
	const [notes, setNotes] = useState("");
	const [params, setParams] = useState<Record<string, string>>({});
	const [preview, setPreview] = useState<string | null>(null);
	const [create, createState] = useCreateAgentFromTemplateMutation();
	const [dryRun, dryRunState] = useCreateAgentFromTemplateMutation();
	const [testEndpoint, testState] = useTestEndpointMutation();
	// Pull mode has no server template: the form writes the agent's YAML through the config route.
	const isPull = tpl?.adapter === "pull";
	const [pullOpts, setPullOpts] = useState<PullOptions>(DEFAULT_PULL_OPTIONS);
	const [putConfig, putState] = usePutAgentConfigMutation();
	// Every template checks the name against the existing agents, so Create never overwrites one.
	const agents = useGetAgentsQuery();
	const takenIds = useMemo(() => new Set((agents.data?.agents ?? []).map((a) => a.id)), [agents.data]);
	const defaultName = tpl ? defaultAgentName(tpl, takenIds) : "";
	// What the person sees in the box (as typed) and the id it is saved as.
	const typed = typedName ?? defaultName;
	const name = agentIdFromName(typed);
	const setName = (v: string) => setTypedName(v);
	const problem = nameProblem(name, takenIds);
	const nameTaken = takenIds.has(name);
	const prevDefault = useRef("");
	// The typed input survives a reload or Back (X-81); secret fields are never stored.
	const [draft, updateDraft, clearDraft] = useFormDraft<NewAgentDraft>("newAgent", EMPTY_AGENT_DRAFT);
	// True once the draft was read back for the current template: only then is the form saved again.
	const [hydrated, setHydrated] = useState(false);
	const restored = useRef(false);
	const [sent, setSent] = useState(false);
	useLeaveGuard(!sent && agentFormDirty(tpl, typedName, notes, params));

	// Picking a template pre-fills the rest, so Create is the only other click.
	useEffect(() => {
		if (!tpl) return;
		// A typed name survives a template switch; only a name equal to the old default follows the new one.
		setTypedName((cur) => (cur === null || cur === prevDefault.current ? null : cur));
		prevDefault.current = defaultName;
		setModel({ provider: (tpl.providers[0] ?? "openai") as ProviderId, model: tpl.default_model });
		if (!restored.current && draft.template === tpl.id) {
			// First template of this visit is the one of the stored draft: put the typed input back.
			setTypedName(draft.typedName);
			setNotes(draft.notes);
			setParams({ ...defaultParams(tpl), ...draft.params });
		} else {
			setParams(defaultParams(tpl));
		}
		restored.current = true;
		setHydrated(true);
		setPullOpts(DEFAULT_PULL_OPTIONS);
		setPreview(null);
		testState.reset();
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [tpl]);

	useEffect(() => {
		if (!tpl || !hydrated || sent) return;
		updateDraft({ template: tpl.id, typedName, notes, params: storableParams(tpl, params) });
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [tpl, hydrated, typedName, notes, params]);

	const nameOk = ID_RE.test(name);
	const missingParam = tpl ? missingParams(tpl, params).length > 0 : true;
	const missing = tpl ? missingFieldLabels(tpl, params) : { labels: [], wrapEither: false };
	const tested = Boolean(testState.data?.ok);
	const pick = (id: string) => {
		void setTemplateId(id);
		// Below lg the form sits under the gallery: bring it into view after a pick.
		if (typeof window !== "undefined" && window.matchMedia?.("(max-width: 1023px)").matches) {
			window.requestAnimationFrame(() => formRef.current?.scrollIntoView({ behavior: "smooth", block: "start" }));
		}
	};
	const { mostUsed, groups } = templateGroups(templates);
	const onlyProvider = tpl && tpl.providers.length === 1 ? tpl.providers[0] : null;
	const providerLabel = (id: string) => BIFROST_PROVIDERS[id]?.name ?? id;
	const row = (x: AgentTemplate) => {
		const on = x.id === tpl?.id;
		const Icon = templateIcon(x);
		return (
			<button
				key={x.id}
				type="button"
				aria-pressed={on}
				onClick={() => pick(x.id)}
				onMouseEnter={() => setHovered(x)}
				onMouseLeave={() => setHovered(null)}
				onFocus={() => setHovered(x)}
				onBlur={() => setHovered(null)}
				className={cn(
					"hover:bg-muted flex h-10 min-w-0 items-center gap-3 rounded-md px-3 text-left",
					on && "bg-muted ring-primary/40 ring-1",
				)}
				data-testid={`template-${x.id}`}
			>
				<span className="bg-muted text-muted-foreground flex size-8 shrink-0 items-center justify-center rounded-lg">
					<Icon className="size-4" aria-hidden="true" />
				</span>
				<span className="min-w-0 flex-1 text-sm font-medium">{x.name}</span>
				{x.builds && (
					<span className="text-muted-foreground" title={tu("gallery.builds")}>
						<Hammer className="size-3.5" aria-hidden="true" />
						<span className="sr-only">{tu("gallery.builds")}</span>
					</span>
				)}
				{on && <Check className="text-primary size-4 shrink-0" aria-label={t("new.selected")} />}
			</button>
		);
	};
	const ownModel = tpl ? picksOwnModel(tpl, params) : false;
	const body = tpl
		? {
				template: tpl.id,
				id: name,
				model: model && !ownModel ? { provider: model.provider, model: model.model } : undefined,
				notes: notes || undefined,
				params: tpl.params.length ? params : undefined,
			}
		: null;

	const onCreate = async () => {
		if (!body) return;
		const res = isPull ? await putConfig({ id: name, yaml: pullAgentYAML(name, pullOpts, notes), create_only: true }) : await create(body);
		if ("data" in res && res.data) {
			// Created: drop the draft and the leave guard before opening the new agent.
			flushSync(() => {
				setSent(true);
				clearDraft();
			});
			if (returnTo === "/evaluate") void navigate({ to: "/evaluate", search: { agent: res.data.id, keep: "1" } as never });
			else void navigate({ to: "/agents/$agentId", params: { agentId: res.data.id } });
		}
	};
	const onPreview = async () => {
		if (preview !== null) return setPreview(null);
		if (!body) return;
		if (isPull) return setPreview(pullAgentYAML(name, pullOpts, notes));
		const res = await dryRun({ ...body, dry_run: true });
		if ("data" in res && res.data) setPreview(res.data.yaml);
	};
	// While the preview is open it follows the form: half a second after the last change it is
	// fetched again (p02-2). previewOpen keeps the effect from running on its own setPreview.
	const previewOpen = useRef(false);
	previewOpen.current = preview !== null;
	const bodyKey = JSON.stringify(body);
	const pullKey = JSON.stringify(pullOpts);
	useEffect(() => {
		if (!previewOpen.current || !body) return;
		const id = setTimeout(async () => {
			if (isPull) setPreview(pullAgentYAML(name, pullOpts, notes));
			else {
				const res = await dryRun({ ...body, dry_run: true });
				if ("data" in res && res.data && previewOpen.current) setPreview(res.data.yaml);
			}
		}, 500);
		return () => clearTimeout(id);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [bodyKey, pullKey, notes, name]);
	const onTest = () => {
		if (body) void testEndpoint({ template: body });
	};
	const error = createState.error ?? putState.error ?? dryRunState.error;

	return (
		<section className={cn(pageWidthClass("read"), "flex flex-col gap-8")} data-page-width="read" data-testid="agents-new-screen">
			<PageHeader
				title={t("new.title")}
				subtitle={t("new.subtitle")}
			/>
			<div className="bg-muted/40 flex min-w-0 flex-col gap-3 rounded-xl border p-4 sm:flex-row sm:items-center" data-testid="agents-new-band">
				<h2 className="text-sm font-semibold">{tx("agent.bandTitle")}</h2>
				<div className="flex flex-wrap gap-2 sm:ml-auto">
					<Button asChild size="sm" variant="outline">
						<Link to="/connect" data-testid="agents-new-connect">
							<Plug className="size-4" aria-hidden="true" />
							{tx("agent.bandEndpoint")}
						</Link>
					</Button>
					<Button asChild size="sm" variant="outline">
						<a href="#platform-import" data-testid="agents-new-import">
							<CloudDownload className="size-4" aria-hidden="true" />
							{tx("agent.bandImport")}
						</a>
					</Button>
					<Button asChild size="sm" variant="outline">
						<Link to="/agents/from-traces" data-testid="agents-new-from-traces">
							<Radio className="size-4" aria-hidden="true" />
							{tx("agent.bandTraces")}
						</Link>
					</Button>
				</div>
			</div>
			{query.isLoading ? (
				<BlockSkeleton rows={6} testId="agents-new-loading" />
			) : query.isError ? (
				<LoadError title={t("new.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="agents-new-error" />
			) : templates.length === 0 ? (
				<EmptyState icon={Bot} message={t("new.empty")} action={{ label: t("new.emptyAction"), to: "/upload" }} testId="agents-new-empty" />
			) : (
				<div className="grid min-w-0 grid-cols-1 items-start gap-8 lg:grid-cols-[minmax(0,1fr)_minmax(0,24rem)]">
					<div className="flex min-w-0 flex-col gap-5">
						<h2 className="sr-only">{tu("gallery.step1")}</h2>
						<PlatformImportCard returnTo={returnTo} />
						{mostUsed.length > 0 && (
							<div className="flex flex-col gap-2">
								<h3 className="text-foreground text-sm font-semibold">{tx("agent.mostUsed")}</h3>
								<div className="grid grid-cols-1 gap-1 md:grid-cols-2 xl:grid-cols-3">{mostUsed.map(row)}</div>
							</div>
						)}
						{groups.map(({ cat, items }) => {
							const CatIcon = CATEGORY_ICON[cat];
							// Q-107: every group starts open, so the whole gallery is in view; a group can still be folded.
							return (
								<details key={cat} className="group/cat flex flex-col gap-2" open>
									<summary className="flex cursor-pointer list-none items-center gap-2 select-none">
										<ChevronRight
											className="text-muted-foreground size-4 transition-transform group-open/cat:rotate-90"
											aria-hidden="true"
										/>
										<h3 className="text-foreground flex items-center gap-2 text-sm font-semibold">
											<CatIcon className="text-muted-foreground size-4" aria-hidden="true" />
											{t(`new.categories.${cat}`)}
											<span className="text-muted-foreground text-xs font-normal tabular-nums" aria-hidden="true">
												{items.length}
											</span>
											<span className="sr-only">
												{" "}
												{items.length}
												{tx("agent.templates")}
											</span>
										</h3>
									</summary>
									<div className="mt-2 grid grid-cols-1 gap-1 md:grid-cols-2 xl:grid-cols-3">{items.map(row)}</div>
								</details>
							);
						})}
					</div>
					<div
						ref={formRef}
						className="bg-card flex min-w-0 scroll-mt-4 flex-col gap-5 rounded-xl border p-6 shadow-sm lg:sticky lg:top-4"
						data-testid="agents-new-form"
					>
						{hovered && hovered.id !== tpl?.id && (
							<p className="bg-muted/50 rounded-md p-3 text-sm" data-testid="template-hover-description">
								<span className="font-medium">{hovered.name}</span>
								<span className="text-muted-foreground">: {hovered.description}</span>
							</p>
						)}
						{tpl ? (
							<div className="flex min-w-0 flex-col gap-2">
								<div className="flex min-w-0 items-center gap-3">
									<span className="bg-muted text-muted-foreground flex size-8 shrink-0 items-center justify-center rounded-lg">
										{(() => {
											const Icon = templateIcon(tpl);
											return <Icon className="size-4" aria-hidden="true" />;
										})()}
									</span>
									<div className="flex min-w-0 flex-col">
										<h2 className="text-base font-semibold tracking-tight">{tpl.name}</h2>
										<span className="text-muted-foreground text-xs">{adapterLabel(tpl.adapter)}</span>
									</div>
								</div>
								<p className="text-muted-foreground text-sm" data-testid="template-description">
									{tpl.description}
								</p>
								{testable(tpl) && canWrite && <p className="text-muted-foreground text-xs">{tx("agent.steps")}</p>}
							</div>
						) : (
							<h2 className="text-base font-semibold tracking-tight">{tu("gallery.step2")}</h2>
						)}
						{!tpl ? (
							<div className="flex flex-col items-center gap-3 py-8 text-center">
								<span className="bg-muted flex size-12 items-center justify-center rounded-full">
									<Bot className="text-muted-foreground size-6" aria-hidden="true" />
								</span>
								<p className="text-muted-foreground text-sm">{tu("gallery.pickFirst")}</p>
							</div>
						) : (
							<form
								className="flex flex-col gap-4"
								onSubmit={(e) => {
									e.preventDefault();
									void onCreate();
								}}
							>
								<label className="flex flex-col gap-1 text-sm">
									<span className="font-medium">{t("new.name")}</span>
									<Input
											value={typed}
										onChange={(e) => setName(e.target.value)}
										aria-invalid={!nameOk || nameTaken}
										data-testid="agent-name"
									/>
									<span className={cn("text-xs", nameOk && !nameTaken ? "text-muted-foreground" : "text-destructive")}>
										{problem || t("new.nameHelp")}
									</span>
									{name && (
										<span className="text-muted-foreground text-xs" data-testid="agent-saved-as">
											{tx("agent.savedAs", { id: name })}
										</span>
									)}
								</label>
								{ownModel ? (
									<p className="text-muted-foreground text-xs" data-testid="agent-own-model">
										{t("new.ownModel")}
									</p>
								) : (
									<div className="flex flex-col gap-1 text-sm">
										<span className="font-medium">{t("new.model")}</span>
										{/* The shared grouped combobox (RK Q-149): one searchable box, providers as groups with their key state. */}
										<GroupedModelPicker
											value={model}
											onChange={setModel}
											providers={tpl.providers?.length ? tpl.providers : undefined}
											label={t("new.model")}
											testId="agent-model"
										/>
										<span className="text-muted-foreground text-xs">
											{onlyProvider ? (
												<>
													{t("new.onlyProviders", { providers: providerLabel(onlyProvider) })}{" "}
													{/* A missing key is said once, by the model picker's own note (Q-125). */}
													{keyed.includes(onlyProvider as ProviderId) && tx("agent.haveKey", { provider: providerLabel(onlyProvider) })}
												</>
											) : (
												t("new.modelHelp")
											)}
										</span>
									</div>
								)}
								{isPull && <PullOptionsFields value={pullOpts} onChange={setPullOpts} />}
								{tpl.params.length > 0 && <ParamFields tpl={tpl} params={params} onChange={setParams} />}
								{tpl.adapter === "remote_http" && <EndpointPreviewPanel body={body} />}
								{testable(tpl) && canWrite && (
									<div className="flex flex-col gap-2" data-testid="endpoint-test">
										<Button
											type="button"
											variant="outline"
											className="w-fit"
											disabled={missingParam || testState.isLoading}
											onClick={onTest}
											data-testid="endpoint-test-button"
										>
											{t("new.test.button")}
										</Button>
										<span className="text-muted-foreground text-xs">{t("new.test.help")}</span>
										<EndpointTestPanel result={testState.data} error={testState.error} loading={testState.isLoading} />
									</div>
								)}
								{error && (
									<p role="alert" className="text-destructive text-sm" data-testid="agents-new-create-error">
										{getBenchErrorMessage(error)}
									</p>
								)}
								{!canWrite && (
									<p className="text-muted-foreground text-xs" data-testid="agents-new-role-note">
										{t("new.memberOnly")}
									</p>
								)}
								<details className="group">
									<summary className="text-muted-foreground hover:text-foreground flex w-fit cursor-pointer list-none items-center gap-1 text-xs">
										<ChevronRight className="size-3.5 transition-transform group-open:rotate-90" aria-hidden="true" />
										{tu("gallery.more")}
									</summary>
									<div className="mt-3 flex flex-col gap-4">
										<label className="flex flex-col gap-1 text-sm">
											<span className="font-medium">{t("new.notes")}</span>
											<textarea
												className="border-input bg-card min-h-16 rounded-md border px-3 py-2 text-sm"
												value={notes}
												maxLength={4000}
												placeholder={t("new.notesPlaceholder")}
												onChange={(e) => setNotes(e.target.value)}
											/>
										</label>
										{tpl.docs && <p className="text-muted-foreground text-xs">{t("new.docs", { docs: tpl.docs })}</p>}
										{canWrite && (
											<Button
												type="button"
												variant="ghost"
												size="sm"
												className="w-fit"
												disabled={!nameOk || missingParam}
												onClick={() => void onPreview()}
											>
												{preview !== null ? t("new.hidePreview") : t("new.preview")}
											</Button>
										)}
										{preview !== null && (
											<pre className="bg-muted max-h-96 overflow-auto rounded-md p-3 font-mono text-xs" data-testid="agent-preview">
												{preview}
											</pre>
										)}
									</div>
								</details>
								{canWrite && (
									<div className="flex flex-col gap-1">
										<Button
											type="submit"
											className="w-full"
											disabled={!nameOk || nameTaken || missingParam || createState.isLoading || putState.isLoading}
											data-testid="agent-create"
										>
											{tested && <CircleCheck className="size-4" aria-label={tx("agent.tested")} />}
											{createState.isLoading || putState.isLoading ? t("new.creating") : t("new.create")}
										</Button>
										{missingParam && (
											<span className="text-muted-foreground text-sm" data-testid="agent-create-missing">
												{[
													missing.labels.length > 0 ? tx("agent.missing", { fields: missing.labels.join(", ") }) : "",
													missing.wrapEither ? tx("agent.wrapEither") : "",
												]
													.filter(Boolean)
													.join(" ")}
											</span>
										)}
									</div>
								)}
							</form>
						)}
					</div>
				</div>
			)}
		</section>
	);
}