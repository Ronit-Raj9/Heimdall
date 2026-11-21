import { ModelPicker, type ModelRef, type ProviderId } from "@/components/models/modelPicker";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState, PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useCreateAgentFromTemplateMutation, useGetAgentTemplatesQuery } from "@/lib/store/apis/agentBuilderApi";
import type { AgentTemplate, TemplateCategory } from "@/lib/types/agentBuilder";
import { cn } from "@/lib/utils";
import { useNavigate } from "@tanstack/react-router";
import { Bot, Check, ChevronRight, Code2, Hammer, Layers, Plug, SquareTerminal, type LucideIcon } from "lucide-react";
import { AgentAvatar, useAdapterLabel } from "../../../agents/views/agentDisplay";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useMemo, useState } from "react";
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

/** A free default name for template t: "my-<template>". */
export function defaultAgentName(t: AgentTemplate): string {
	return `my-${t.id}`;
}

/**
 * New agent wizard (/agents/new?template=): pick a template (one click), the name and model are
 * pre-filled, Create writes agents/<name>.yaml through the project store and opens the agent.
 */
export default function NewAgentView() {
	const { t } = useTranslation("agentBuilder");
	const { t: tu } = useTranslation("agentsUx");
	const adapterLabel = useAdapterLabel();
	const canWrite = canUse(useSession(), "member");
	const navigate = useNavigate();
	const query = useGetAgentTemplatesQuery();
	const [templateId, setTemplateId] = useQueryState("template", parseAsString.withOptions({ history: "replace" }));
	// ?return=/evaluate: the /evaluate wizard sent the user here and gets the new agent back.
	const [returnTo] = useQueryState("return", parseAsString);
	const templates = useMemo(() => query.data?.templates ?? [], [query.data]);
	const tpl = templates.find((x) => x.id === templateId) ?? null;

	const [name, setName] = useState("");
	const [model, setModel] = useState<ModelRef | null>(null);
	const [notes, setNotes] = useState("");
	const [params, setParams] = useState<Record<string, string>>({});
	const [preview, setPreview] = useState<string | null>(null);
	const [create, createState] = useCreateAgentFromTemplateMutation();
	const [dryRun, dryRunState] = useCreateAgentFromTemplateMutation();

	// Picking a template pre-fills the rest, so Create is the only other click.
	useEffect(() => {
		if (!tpl) return;
		setName(defaultAgentName(tpl));
		setModel({ provider: (tpl.providers[0] ?? "openai") as ProviderId, model: tpl.default_model });
		setParams({});
		setPreview(null);
	}, [tpl]);

	const nameOk = ID_RE.test(name);
	const missingParam = tpl?.params.some((p) => p.required && !(params[p.name] ?? "").trim()) ?? true;
	const body = tpl
		? {
				template: tpl.id,
				id: name,
				model: model ? { provider: model.provider, model: model.model } : undefined,
				notes: notes || undefined,
				params: tpl.params.length ? params : undefined,
			}
		: null;

	const onCreate = async () => {
		if (!body) return;
		const res = await create(body);
		if ("data" in res && res.data) {
			if (returnTo === "/evaluate") void navigate({ to: "/evaluate", search: { agent: res.data.id } as never });
			else void navigate({ to: "/agents/$agentId", params: { agentId: res.data.id } });
		}
	};
	const onPreview = async () => {
		if (preview !== null) return setPreview(null);
		if (!body) return;
		const res = await dryRun({ ...body, dry_run: true });
		if ("data" in res && res.data) setPreview(res.data.yaml);
	};
	const error = createState.error ?? dryRunState.error;

	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-8" data-testid="agents-new-screen">
			<PageHeader title={t("new.title")} subtitle={t("new.subtitle")} />
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
					<div className="bg-card flex min-w-0 flex-col gap-5 rounded-xl border p-6 shadow-sm lg:sticky lg:top-4" data-testid="agents-new-form">
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
										aria-invalid={!nameOk}
										data-testid="agent-name"
									/>
									<span className={cn("text-xs", nameOk ? "text-muted-foreground" : "text-destructive")}>
										{nameOk ? t("new.nameHelp") : t("new.nameInvalid")}
									</span>
								</label>
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
								{tpl.params.map((p) => (
									<label key={p.name} className="flex flex-col gap-1 text-sm">
										<span className="font-medium">{p.label}</span>
										<Input
											value={params[p.name] ?? ""}
											placeholder={p.placeholder}
											required={p.required}
											onChange={(e) => setParams((cur) => ({ ...cur, [p.name]: e.target.value }))}
											data-testid={`param-${p.name}`}
										/>
										{p.help && <span className="text-muted-foreground text-xs">{p.help}</span>}
									</label>
								))}
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
											<Button type="button" variant="ghost" size="sm" className="w-fit" disabled={!nameOk || missingParam} onClick={() => void onPreview()}>
												{preview !== null ? t("new.hidePreview") : t("new.preview")}
											</Button>
										)}
									</div>
								</details>
								{canWrite && (
									<Button type="submit" className="w-full" disabled={!nameOk || missingParam || createState.isLoading} data-testid="agent-create">
										{createState.isLoading ? t("new.creating") : t("new.create")}
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
