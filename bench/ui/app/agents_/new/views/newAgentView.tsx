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
import { Bot, Check, Hammer } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

const CATEGORIES: TemplateCategory[] = ["cli", "framework", "starter", "bring-your-own"];
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
	const canWrite = canUse(useSession(), "member");
	const navigate = useNavigate();
	const query = useGetAgentTemplatesQuery();
	const [templateId, setTemplateId] = useQueryState("template", parseAsString.withOptions({ history: "replace" }));
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
			void navigate({ to: "/agents/$agentId", params: { agentId: res.data.id } });
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
		<section className="flex min-w-0 flex-col gap-6" data-testid="agents-new-screen">
			<PageHeader title={t("new.title")} subtitle={t("new.subtitle")} />
			{query.isLoading ? (
				<BlockSkeleton rows={6} testId="agents-new-loading" />
			) : query.isError ? (
				<LoadError title={t("new.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="agents-new-error" />
			) : templates.length === 0 ? (
				<EmptyState icon={Bot} message={t("new.empty")} action={{ label: t("new.emptyAction"), to: "/upload" }} testId="agents-new-empty" />
			) : (
				<div className="grid min-w-0 grid-cols-1 items-start gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(0,28rem)]">
					<div className="flex min-w-0 flex-col gap-5">
						<h2 className="text-sm font-semibold">{t("new.step1")}</h2>
						{CATEGORIES.map((cat) => {
							const items = templates.filter((x) => x.category === cat);
							if (!items.length) return null;
							return (
								<div key={cat} className="flex flex-col gap-2">
									<h3 className="text-muted-foreground text-xs font-medium tracking-wide uppercase">{t(`new.categories.${cat}`)}</h3>
									<div className="grid grid-cols-1 gap-2 sm:grid-cols-2 2xl:grid-cols-3">
										{items.map((x) => {
											const on = x.id === tpl?.id;
											return (
												<button
													key={x.id}
													type="button"
													aria-pressed={on}
													onClick={() => void setTemplateId(x.id)}
													className={cn(
														"hover:border-primary/60 flex flex-col items-start gap-1 rounded-md border p-3 text-left text-sm transition-colors",
														on && "border-primary bg-primary/5",
													)}
													data-testid={`template-${x.id}`}
												>
													<span className="flex w-full items-center justify-between gap-2 font-medium">
														{x.name}
														{on && <Check className="text-primary size-4" aria-label={t("new.selected")} />}
													</span>
													<span className="text-muted-foreground text-xs">{x.description}</span>
													<span className="text-muted-foreground mt-1 flex items-center gap-1 text-[11px]">
														{x.builds && <Hammer className="size-3" aria-hidden="true" />}
														{x.adapter} · {x.builds ? t("new.builds") : t("new.noBuild")}
													</span>
												</button>
											);
										})}
									</div>
								</div>
							);
						})}
					</div>
					<div className="flex min-w-0 flex-col gap-4 rounded-md border p-4 xl:sticky xl:top-4" data-testid="agents-new-form">
						<h2 className="text-sm font-semibold">{t("new.step2")}</h2>
						{!tpl ? (
							<p className="text-muted-foreground text-sm">{t("new.pickFirst")}</p>
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
								{canWrite && <div className="flex flex-wrap gap-2">
									<Button type="submit" disabled={!nameOk || missingParam || createState.isLoading} data-testid="agent-create">
										{createState.isLoading ? t("new.creating") : t("new.create")}
									</Button>
									<Button type="button" variant="outline" disabled={!nameOk || missingParam} onClick={() => void onPreview()}>
										{preview !== null ? t("new.hidePreview") : t("new.preview")}
									</Button>
								</div>}
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
