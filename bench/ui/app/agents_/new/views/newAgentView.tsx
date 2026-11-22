import { ModelPicker, type ModelRef, type ProviderId } from "@/components/models/modelPicker";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState, PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import i18n from "@/lib/i18n";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
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
import { Bot, Check, ChevronRight, Code2, Hammer, Layers, Plug, SquareTerminal, type LucideIcon } from "lucide-react";
import { AgentAvatar, useAdapterLabel } from "../../../agents/views/agentDisplay";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useMemo, useState } from "react";
import {
	DEFAULT_PULL_OPTIONS,
	defaultParams,
	EndpointPreviewPanel,
	EndpointTestPanel,
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

/** Why name cannot be used for a new agent, or "" when it can. */
export function nameProblem(name: string, taken: ReadonlySet<string>): string {
	if (!ID_RE.test(name)) return i18n.t("new.nameInvalid", { ns: "agentBuilder" });
	if (taken.has(name)) return i18n.t("new.nameExists", { ns: "agentBuilder", name });
	return "";
}

/**
 * New agent wizard (/agents/new?template=): pick a template (one click), the name and model are
 * pre-filled, Create writes agents/<name>.yaml through the project store and opens the agent.
 */
export default function NewAgentView() {
	const { t } = useTranslation("agentBuilder");
	const { t: tu } = useTranslation("agentsUx");
	const { t: tc } = useTranslation("connectTraces");
	const adapterLabel = useAdapterLabel();
	const canWrite = canUse(useSession(), "member");
	const navigate = useNavigate();
	const query = useGetAgentTemplatesQuery();
	const [templateId, setTemplateId] = useQueryState("template", parseAsString.withOptions({ history: "replace" }));
	// ?return=/evaluate: the /evaluate wizard sent the user here and gets the new agent back.
	const [returnTo] = useQueryState("return", parseAsString);
	const templates = useMemo(() => withPullTemplate(query.data?.templates ?? []), [query.data]);
	const tpl = templates.find((x) => x.id === templateId) ?? null;

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
	const name = typedName ?? (tpl ? defaultAgentName(tpl, takenIds) : "");
	const setName = (v: string) => setTypedName(v);
	const problem = nameProblem(name, takenIds);
	const nameTaken = takenIds.has(name);

	// Picking a template pre-fills the rest, so Create is the only other click.
	useEffect(() => {
		if (!tpl) return;
		setTypedName(null);
		setModel({ provider: (tpl.providers[0] ?? "openai") as ProviderId, model: tpl.default_model });
		setParams(defaultParams(tpl));
		setPullOpts(DEFAULT_PULL_OPTIONS);
		setPreview(null);
		testState.reset();
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [tpl]);

	const nameOk = ID_RE.test(name);
	const missingParam = tpl ? missingParams(tpl, params).length > 0 : true;
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
			if (returnTo === "/evaluate") void navigate({ to: "/evaluate", search: { agent: res.data.id } as never });
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
	const onTest = () => {
		if (body) void testEndpoint({ template: body });
	};
	const error = createState.error ?? putState.error ?? dryRunState.error;

	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-8" data-testid="agents-new-screen">
			<PageHeader
				title={t("new.title")}
				subtitle={t("new.subtitle")}
				actions={
					<Link
						to="/agents/from-traces"
						title={tc("entryHint")}
						className="text-primary text-xs font-medium underline underline-offset-2"
						data-testid="agents-new-from-traces"
					>
						{tc("entry")}
					</Link>
				}
			/>
			{query.isLoading ? (
				<BlockSkeleton rows={6} testId="agents-new-loading" />
			) : query.isError ? (
				<LoadError title={t("new.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="agents-new-error" />
			) : templates.length === 0 ? (
				<EmptyState icon={Bot} message={t("new.empty")} action={{ label: t("new.emptyAction"), to: "/upload" }} testId="agents-new-empty" />
			) : (
				<div className="grid min-w-0 grid-cols-1 items-start gap-8 lg:grid-cols-[minmax(0,1fr)_minmax(0,24rem)]">
					<div className="flex min-w-0 flex-col gap-8">
						<h2 className="sr-only">{tu("gallery.step1")}</h2>
						<PlatformImportCard returnTo={returnTo} />
						{CATEGORIES.map((cat) => {
							const items = templates.filter((x) => x.category === cat);
							if (!items.length) return null;
							const CatIcon = CATEGORY_ICON[cat];
							return (
								<div key={cat} className="flex flex-col gap-3">
									<h3 className="text-foreground flex items-center gap-2 text-sm font-semibold">
										<CatIcon className="text-muted-foreground size-4" aria-hidden="true" />
										{t(`new.categories.${cat}`)}
										<span className="text-muted-foreground font-normal tabular-nums">{items.length}</span>
									</h3>
									<div className="grid grid-cols-1 gap-3 sm:grid-cols-2 2xl:grid-cols-3">
										{items.map((x) => {
											const on = x.id === tpl?.id;
											return (
												<button
													key={x.id}
													type="button"
													aria-pressed={on}
													onClick={() => void setTemplateId(x.id)}
													className={cn(
														"bg-card hover:border-primary/50 flex min-w-0 items-start gap-3 rounded-xl border p-4 text-left transition hover:-translate-y-0.5 hover:shadow-md",
														on && "border-primary ring-primary/30 ring-2",
													)}
													data-testid={`template-${x.id}`}
												>
													<AgentAvatar name={x.name} />
													<span className="flex min-w-0 flex-1 flex-col gap-1">
														<span className="flex w-full items-center justify-between gap-2 text-sm font-semibold">
															<span className="truncate">{x.name}</span>
															{on && <Check className="text-primary size-4 shrink-0" aria-label={t("new.selected")} />}
														</span>
														<span className="text-muted-foreground line-clamp-2 text-xs">{x.description}</span>
														<span className="text-muted-foreground mt-1 flex items-center gap-1 text-[11px]">
															{adapterLabel(x.adapter)}
															{x.builds && (
																<span className="inline-flex items-center gap-1" title={tu("gallery.builds")}>
																	<span aria-hidden="true">·</span>
																	<Hammer className="size-3" aria-hidden="true" />
																	<span className="sr-only">{tu("gallery.builds")}</span>
																</span>
															)}
														</span>
													</span>
												</button>
											);
										})}
									</div>
								</div>
							);
						})}
					</div>
					<div
						className="bg-card flex min-w-0 flex-col gap-5 rounded-xl border p-6 shadow-sm lg:sticky lg:top-4"
						data-testid="agents-new-form"
					>
						{tpl ? (
							<div className="flex min-w-0 items-center gap-3">
								<AgentAvatar name={tpl.name} />
								<div className="flex min-w-0 flex-col">
									<h2 className="truncate text-base font-semibold tracking-tight">{tpl.name}</h2>
									<span className="text-muted-foreground text-xs">{adapterLabel(tpl.adapter)}</span>
								</div>
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
										value={name}
										onChange={(e) => setName(e.target.value.toLowerCase())}
										aria-invalid={!nameOk || nameTaken}
										data-testid="agent-name"
									/>
									<span className={cn("text-xs", nameOk && !nameTaken ? "text-muted-foreground" : "text-destructive")}>
										{problem || t("new.nameHelp")}
									</span>
								</label>
								{ownModel ? (
									<p className="text-muted-foreground text-xs" data-testid="agent-own-model">
										{t("new.ownModel")}
									</p>
								) : (
									<div className="flex flex-col gap-1 text-sm">
										<span className="font-medium">{t("new.model")}</span>
										<ModelPicker
											value={model}
											onChange={setModel}
											purpose="agent"
											allowedProviders={tpl.providers as ProviderId[]}
											testId="agent-model"
										/>
										<span className="text-muted-foreground text-xs">
											{tpl.providers.length === 1 ? t("new.onlyProviders", { providers: tpl.providers.join(", ") }) : t("new.modelHelp")}
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
									</div>
								</details>
								{canWrite && (
									<Button
										type="submit"
										className="w-full"
										disabled={!nameOk || nameTaken || missingParam || createState.isLoading || putState.isLoading}
										data-testid="agent-create"
									>
										{createState.isLoading || putState.isLoading ? t("new.creating") : t("new.create")}
									</Button>
								)}
								{preview !== null && (
									<pre className="bg-muted max-h-96 overflow-auto rounded-md p-3 font-mono text-xs" data-testid="agent-preview">
										{preview}
									</pre>
								)}
							</form>
						)}
					</div>
				</div>
			)}
		</section>
	);
}