import { PROVIDERS, type ProviderId } from "@/components/models/modelPicker";
import {
	BIFROST_PROVIDERS,
	familyServing,
	groupProviders,
	keyUsable,
	providerGroup,
	providerInitials,
	providerKeyState,
	providerName,
	type ProviderGroup,
	type ProviderKeyState,
} from "@/components/models/providerCatalog";
import { ConfirmButton } from "@/components/platform/badges";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	useCreateModelKeyMutation,
	useDeleteModelKeyMutation,
	useGetModelProvidersQuery,
	useGetModelsQuery,
	useTestModelKeyMutation,
	useUpdateModelKeyMutation,
} from "@/lib/store/apis/modelsApi";
import type {
	BifrostProviderSpec,
	ModelKey,
	ModelKeyPrice,
	ModelKeyRequest,
	ModelKeyTestResult,
	ModelProviderInfo,
} from "@/lib/types/models";
import { Link, useSearch } from "@tanstack/react-router";
import { useEmbedded } from "@/components/shell/embedded";
import { cn } from "@/lib/utils";
import { KeyRound, Pencil, Plus, Sparkles, X } from "lucide-react";
import { useEffect, useId, useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

const STATUS_VARIANT = {
	untested: "outline",
	ok: "success",
	invalid: "destructive",
	error: "warning",
} as const;

/** The provider named by ?add=<provider>, when it is one we know (the server's list, else the Bifrost list). */
export function addParam(search: unknown, known?: readonly string[]): ProviderId | null {
	const add = (search as { add?: unknown } | undefined)?.add;
	if (typeof add !== "string") return null;
	const ids = known && known.length ? known : [...PROVIDERS, ...Object.keys(BIFROST_PROVIDERS)];
	return ids.includes(add) ? add : null;
}

/** Comma or newline separated model list to an array. */
export function parseModels(text: string): string[] {
	return text
		.split(/[,\n]/)
		.map((s) => s.trim())
		.filter(Boolean);
}

export function formatUSD(v: number): string {
	return `$${v.toFixed(2)}`;
}

/** Tile tint per provider group. */
const GROUP_TINT: Record<ProviderGroup, string> = {
	frontier: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
	"open-hosts": "bg-violet-500/10 text-violet-700 dark:text-violet-300",
	cloud: "bg-blue-500/10 text-blue-700 dark:text-blue-300",
	"self-hosted": "bg-amber-500/10 text-amber-700 dark:text-amber-300",
	custom: "bg-slate-500/10 text-slate-700 dark:text-slate-300",
	other: "bg-slate-500/10 text-slate-700 dark:text-slate-300",
};

const PROVIDER_STATUS_VARIANT: Record<ProviderKeyState, "success" | "outline" | "destructive" | "neutral"> = {
	ok: "success",
	untested: "outline",
	"over-budget": "destructive",
	failed: "destructive",
	none: "neutral",
};

/** Whether a provider's key form must have a base URL. */
export function needsBaseUrl(info?: Pick<ModelProviderInfo, "needs_base_url" | "custom_base_url">): boolean {
	return Boolean(info?.needs_base_url || info?.custom_base_url);
}

/** Providers whose id, name or group label contains the filter text. */
export function filterProviders<T extends Pick<ModelProviderInfo, "id" | "name">>(
	providers: readonly T[],
	filter: string,
	groupLabel: (g: ProviderGroup) => string,
): T[] {
	const f = filter.trim().toLowerCase();
	if (!f) return [...providers];
	return providers.filter((p) => `${p.id} ${p.name} ${groupLabel(providerGroup(p))}`.toLowerCase().includes(f));
}

const STATUS_DOT = {
	untested: "bg-muted-foreground/50",
	ok: "bg-chart-success",
	invalid: "bg-chart-error",
	error: "bg-chart-warning",
} as const;

/** Spend as a share of budget, 0..100; 0 when there is no budget. */
export function spendPercent(spent: number, budget: number): number {
	if (budget <= 0) return 0;
	return Math.max(0, Math.min(100, (spent / budget) * 100));
}

/** Models & keys: one card per provider with its keys (add, edit, test, delete) and the default model. */
export default function ModelsView() {
	const { t } = useTranslation("models");
	const { t: tu } = useTranslation("u11");
	const session = useSession();
	const search = useSearch({ strict: false });
	const embedded = useEmbedded();
	const Heading = embedded ? "h2" : "h1";
	const models = useGetModelsQuery();
	const isAdmin = canUse(session, "admin");
	const canTest = canUse(session, "member");
	const providers = models.data?.providers ?? [];
	const keys = models.data?.keys ?? [];
	const initialAdd = addParam(
		search,
		providers.map((p) => p.id),
	);
	const [filter, setFilter] = useState("");
	const [form, setForm] = useState<{
		provider: ProviderId;
		key?: ModelKey;
	} | null>(initialAdd && isAdmin ? { provider: initialAdd } : null);

	const listed: ModelProviderInfo[] = providers.length
		? providers
		: PROVIDERS.map((id) => ({ id, name: providerName(id, []), needs_base_url: id === "openai-compatible", key_hint: "", models: [] }));
	const groups = groupProviders(filterProviders(listed, filter, (g) => t(`groups.${g}`)));
	const firstProvider: ProviderId = listed[0]?.id ?? "openai";

	return (
		<div className="mx-auto flex w-full max-w-[1200px] flex-col gap-8 p-4 sm:p-6" data-testid="models-view">
			<header className="flex flex-wrap items-start justify-between gap-4">
				<div className="flex min-w-0 flex-col gap-1">
					<Heading className={embedded ? "text-xl font-semibold tracking-tight" : "text-2xl font-semibold tracking-tight"}>{t("title")}</Heading>
					<p className="text-muted-foreground text-sm">{tu("models.subtitle")}</p>
					<p className="text-muted-foreground text-xs">
						{t("secretsHint")}{" "}
						<Link
							to="/settings"
							search={{ tab: "secrets" } as never}
							className="text-foreground underline-offset-4 hover:underline"
							data-testid="models-secrets-link"
						>
							{t("secretsLink")}
						</Link>
					</p>
				</div>
				{isAdmin && models.data?.keys_ready && (
					<Button size="sm" className="gap-1.5" onClick={() => setForm({ provider: firstProvider })} data-testid="models-add">
						<Plus className="size-4" aria-hidden="true" />
						{tu("models.addKey")}
					</Button>
				)}
			</header>

			{models.isLoading ? (
				<BlockSkeleton rows={4} testId="models-loading" />
			) : models.isError || !models.data ? (
				<LoadError title={t("loadError")} error={models.error} onRetry={() => void models.refetch()} testId="models-error" />
			) : !models.data.keys_ready ? (
				<EmptyState
					icon={KeyRound}
					message={t("unavailable")}
					action={{
						label: t("unavailableAction"),
						to: "/docs",
						search: { doc: "hosting" },
					}}
					testId="models-unavailable"
				/>
			) : (
				<>
					<div className="bg-card flex flex-wrap items-center gap-3 rounded-xl border px-5 py-4" data-testid="models-default">
						<span className="bg-primary/10 text-primary flex size-8 items-center justify-center rounded-lg">
							<Sparkles className="size-4" aria-hidden="true" />
						</span>
						{models.data.default ? (
							<div className="flex min-w-0 flex-col">
								<span className="text-muted-foreground text-xs">{tu("models.defaultLabel")}</span>
								<span className="truncate text-sm font-medium">
									{models.data.default.model}
									<span className="text-muted-foreground font-normal"> · {providerName(models.data.default.provider, providers)}</span>
								</span>
							</div>
						) : (
							<span className="text-muted-foreground text-sm">{t("noDefault")}</span>
						)}
						<span
							className="text-muted-foreground ml-auto text-xs underline decoration-dotted underline-offset-4"
							title={t("priceHintTitle")}
							data-testid="models-price-hint"
						>
							{t("priceHint")}
						</span>
					</div>
					{keys.length === 0 && (
						<EmptyState
							icon={KeyRound}
							message={isAdmin ? t("empty") : t("emptyViewer")}
							action={
								isAdmin
									? {
											label: t("add"),
											onClick: () => setForm({ provider: firstProvider }),
										}
									: { label: t("emptyViewerAction"), to: "/experiments" }
							}
							testId="models-empty"
						/>
					)}
					<div className="flex flex-col gap-6" data-testid="models-table">
						<div className="flex flex-wrap items-center justify-between gap-3">
							<h2 className="text-lg font-semibold">{t("providersTitle")}</h2>
							<Input
								type="search"
								autoComplete="off"
								className="h-8 w-56"
								value={filter}
								placeholder={t("filterProviders")}
								aria-label={t("filterProviders")}
								onChange={(e) => setFilter(e.target.value)}
								data-testid="models-filter"
							/>
						</div>
						{groups.length === 0 && <p className="text-muted-foreground text-sm">{t("noProviderMatch")}</p>}
						{groups.map((g) => (
							<section key={g.group} className="flex flex-col gap-3" data-testid={`provider-group-${g.group}`}>
								<div className="flex flex-col gap-0.5">
									<h3 className="text-sm font-semibold">{t(`groups.${g.group}`)}</h3>
									<p className="text-muted-foreground text-xs">{t(`groupHints.${g.group}`)}</p>
								</div>
								<div className="grid gap-4 md:grid-cols-2">
									{g.providers.map((p) => (
										<ProviderCard
											key={p.id}
											provider={p.id}
											name={p.name || providerName(p.id, providers)}
											group={g.group}
											keys={keys.filter((k) => k.provider === p.id)}
											isAdmin={isAdmin}
											canTest={canTest}
											onAdd={() => setForm({ provider: p.id })}
											onEdit={(k) => setForm({ provider: k.provider, key: k })}
										/>
									))}
								</div>
							</section>
						))}
					</div>
					<OpenModels providers={providers} keys={keys} />
					{form && (
						<SidePanel title={form.key ? t("editTitle") : t("addTitle")} onClose={() => setForm(null)}>
							<KeyForm
								key={`${form.provider}-${form.key?.id ?? "new"}`}
								providers={providers}
								initial={form}
								onDone={() => setForm(null)}
							/>
						</SidePanel>
					)}
				</>
			)}
		</div>
	);
}

function ProviderTile({ provider, group, className }: { provider: ProviderId; group: ProviderGroup; className?: string }) {
	return (
		<span
			className={cn(
				"flex size-10 shrink-0 items-center justify-center rounded-lg text-sm font-semibold tracking-tight",
				GROUP_TINT[group],
				className,
			)}
			aria-hidden="true"
		>
			{providerInitials(provider)}
		</span>
	);
}

/** Open model families and which providers in the catalog serve them. */
function OpenModels({ providers, keys }: { providers: ModelProviderInfo[]; keys: ModelKey[] }) {
	const { t } = useTranslation("models");
	const rows = familyServing(providers);
	const keyed = new Set(keys.filter(keyUsable).map((k) => k.provider));
	return (
		<section className="flex flex-col gap-3" data-testid="open-models">
			<div className="flex flex-col gap-0.5">
				<h2 className="text-lg font-semibold">{t("open.title")}</h2>
				<p className="text-muted-foreground text-sm">{t("open.subtitle")}</p>
			</div>
			<ul className="bg-card flex flex-col divide-y rounded-xl border">
				{rows.map(({ family, providers: serving }) => (
					<li
						key={family.id}
						className="flex flex-col gap-1.5 px-5 py-3 sm:flex-row sm:items-start sm:gap-4"
						data-testid={`open-family-${family.id}`}
					>
						<div className="flex w-40 shrink-0 flex-col">
							<span className="text-sm font-medium">{family.name}</span>
							<span className="text-muted-foreground text-xs">{family.maker}</span>
						</div>
						<div className="flex min-w-0 flex-1 flex-col gap-1">
							{serving.length > 0 ? (
								<div className="flex flex-wrap items-center gap-1.5">
									<span className="text-muted-foreground text-xs">{t("open.servedBy")}</span>
									{serving.map((p) => (
										<Badge
											key={p.id}
											variant={keyed.has(p.id) ? "success" : "outline"}
											className="rounded-full px-2"
											title={p.models.join(", ") || undefined}
											data-testid={`open-family-${family.id}-${p.id}`}
										>
											{p.name}
											{keyed.has(p.id) && <span className="sr-only"> ({t("open.hasKey")})</span>}
										</Badge>
									))}
								</div>
							) : (
								<span className="text-muted-foreground text-xs">{t("open.none")}</span>
							)}
							{serving.length > 0 && (
								<span className="text-muted-foreground truncate font-mono text-[11px]" title={serving.flatMap((p) => p.models).join(", ")}>
									{[...new Set(serving.flatMap((p) => p.models))].slice(0, 6).join(", ")}
								</span>
							)}
							{!family.nativeProvider && <span className="text-muted-foreground text-xs">{t("open.custom", { maker: family.maker })}</span>}
						</div>
					</li>
				))}
			</ul>
		</section>
	);
}

function ProviderCard({
	provider,
	name,
	group,
	keys,
	isAdmin,
	canTest,
	onAdd,
	onEdit,
}: {
	provider: ProviderId;
	name: string;
	group: ProviderGroup;
	keys: ModelKey[];
	isAdmin: boolean;
	canTest: boolean;
	onAdd(): void;
	onEdit(k: ModelKey): void;
}) {
	const { t } = useTranslation("models");
	const { t: tu } = useTranslation("u11");
	const state = providerKeyState(keys);
	return (
		<section
			className="bg-card flex flex-col gap-4 rounded-xl border p-5 transition-shadow hover:shadow-md"
			data-testid={`provider-card-${provider}`}
		>
			<div className="flex items-center gap-3">
				<ProviderTile provider={provider} group={group} />
				<div className="flex min-w-0 flex-1 flex-col">
					<h2 className="truncate text-base font-semibold">{name}</h2>
					<span className="text-muted-foreground text-xs">
						{keys.length ? tu("models.keysCount", { count: keys.length }) : tu("models.noKeys")}
					</span>
				</div>
				<Badge variant={PROVIDER_STATUS_VARIANT[state]} className="rounded-full px-2" data-testid={`provider-status-${provider}`}>
					{t(`providerStatus.${state}`)}
				</Badge>
				{isAdmin && (
					<Button size="sm" variant="ghost" className="gap-1" onClick={onAdd} data-testid={`provider-add-${provider}`}>
						<Plus className="size-3.5" aria-hidden="true" />
						{tu("models.addKey")}
					</Button>
				)}
			</div>
			{keys.length > 0 && (
				<ul className="flex flex-col divide-y rounded-lg border">
					{keys.map((k) => (
						<KeyRow key={k.id} k={k} isAdmin={isAdmin} canTest={canTest} onEdit={() => onEdit(k)} />
					))}
				</ul>
			)}
		</section>
	);
}

function KeyRow({ k, isAdmin, canTest, onEdit }: { k: ModelKey; isAdmin: boolean; canTest: boolean; onEdit(): void }) {
	const { t } = useTranslation("models");
	const { t: tu } = useTranslation("u11");
	const [test, testState] = useTestModelKeyMutation();
	const [remove, removeState] = useDeleteModelKeyMutation();
	const [result, setResult] = useState<ModelKeyTestResult | null>(null);
	const runTest = async () => {
		try {
			const r = await test(k.id).unwrap();
			setResult(r.result);
		} catch (err) {
			setResult({
				ok: false,
				status: "error",
				message: getBenchErrorMessage(err),
				latency_ms: 0,
			});
		}
	};
	const pct = spendPercent(k.month_spent_usd, k.budget_usd);
	const variant = STATUS_VARIANT[k.status] ?? "outline";
	return (
		<li className="flex flex-col gap-3 p-4" data-testid={`model-key-${k.id}`}>
			<div className="flex flex-wrap items-center gap-2">
				<span className="min-w-0 truncate text-sm font-medium" title={k.label || k.provider}>
					{k.label || k.provider}
				</span>
				<span className="text-muted-foreground font-mono text-xs">{k.last4 ? `...${k.last4}` : ""}</span>
				<Badge variant={variant} className="rounded-full px-2" data-testid={`model-key-status-${k.id}`}>
					<span className={cn("size-1.5 rounded-full", STATUS_DOT[k.status] ?? STATUS_DOT.untested)} aria-hidden="true" />
					{t(`status.${k.status}`)}
				</Badge>
				{k.over_budget && (
					<Badge variant="destructive" className="rounded-full px-2">
						{t("overBudget")}
					</Badge>
				)}
				<div className="ml-auto flex items-center gap-1">
					{canTest && (
						<Button
							size="sm"
							variant="outline"
							className="h-7 px-2 text-xs"
							disabled={testState.isLoading}
							onClick={() => void runTest()}
							data-testid={`model-key-test-${k.id}`}
						>
							{testState.isLoading ? t("testing") : t("test")}
						</Button>
					)}
					{isAdmin && (
						<>
							<Button
								size="sm"
								variant="ghost"
								className="h-7 px-2"
								onClick={onEdit}
								aria-label={t("edit")}
								title={t("edit")}
								data-testid={`model-key-edit-${k.id}`}
							>
								<Pencil className="size-3.5" aria-hidden="true" />
							</Button>
							<ConfirmButton
								label={t("delete")}
								question={t("confirmDelete", { label: k.label || k.provider })}
								confirmLabel={t("delete")}
								cancelLabel={t("cancel")}
								onConfirm={() => void remove(k.id)}
								isLoading={removeState.isLoading}
								testId={`model-key-delete-${k.id}`}
							/>
						</>
					)}
				</div>
			</div>
			<div className="flex flex-col gap-1.5">
				<div className="text-muted-foreground flex items-center justify-between gap-2 text-xs">
					<span className="truncate" title={k.models.join(", ")}>
						{k.models.length ? k.models.join(", ") : tu("models.anyModel")}
					</span>
					<span className="shrink-0 tabular-nums">
						{k.budget_usd > 0
							? tu("models.spent", {
									spent: formatUSD(k.month_spent_usd),
									budget: formatUSD(k.budget_usd),
								})
							: tu("models.spentNoLimit", {
									spent: formatUSD(k.month_spent_usd),
								})}
					</span>
				</div>
				{k.price && (
					<span className="text-muted-foreground text-xs tabular-nums" data-testid={`model-key-price-${k.id}`}>
						{t("priceShown", {
							input: formatUSD(k.price.input_per_mtok),
							output: formatUSD(k.price.output_per_mtok),
							cache: formatUSD(k.price.cache_per_mtok || k.price.input_per_mtok),
						})}
					</span>
				)}
				{k.budget_usd > 0 && (
					<div
						className="bg-muted h-1.5 overflow-hidden rounded-full"
						role="progressbar"
						aria-valuemin={0}
						aria-valuemax={100}
						aria-valuenow={Math.round(pct)}
						aria-label={t("colSpend")}
					>
						<div
							className={cn(
								"h-full rounded-full transition-all",
								k.over_budget ? "bg-chart-error" : pct > 80 ? "bg-chart-warning" : "bg-chart-success",
							)}
							style={{ width: `${pct}%` }}
						/>
					</div>
				)}
			</div>
			{result && (
				<p
					role={result.ok ? "status" : "alert"}
					className={`text-xs ${result.ok ? "text-chart-success-ink" : "text-chart-error-ink"}`}
					data-testid={`model-key-result-${k.id}`}
				>
					{result.message}
				</p>
			)}
			{removeState.error && (
				<p role="alert" className="text-chart-error-ink text-xs">
					{getBenchErrorMessage(removeState.error)}
				</p>
			)}
		</li>
	);
}

/** A right-hand side panel over the page; Escape or the backdrop closes it. */
function SidePanel({ title, onClose, children }: { title: string; onClose(): void; children: ReactNode }) {
	const { t: tu } = useTranslation("u11");
	const titleId = useId();
	useEffect(() => {
		const onKey = (e: KeyboardEvent) => {
			if (e.key === "Escape") onClose();
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [onClose]);
	return (
		<div className="fixed inset-0 z-50 flex justify-end" data-testid="model-key-panel">
			<button
				type="button"
				className="absolute inset-0 cursor-default bg-black/40 backdrop-blur-[1px]"
				aria-label={tu("models.close")}
				onClick={onClose}
				tabIndex={-1}
			/>
			<div
				role="dialog"
				aria-modal="true"
				aria-labelledby={titleId}
				className="bg-background relative flex h-full w-full max-w-md flex-col border-l shadow-xl"
			>
				<div className="flex items-center justify-between gap-3 border-b px-6 py-4">
					<h2 id={titleId} className="text-base font-semibold">
						{title}
					</h2>
					<Button size="sm" variant="ghost" className="h-8 w-8 p-0" onClick={onClose} aria-label={tu("models.close")}>
						<X className="size-4" aria-hidden="true" />
					</Button>
				</div>
				<div className="flex-1 overflow-y-auto px-6 py-5">{children}</div>
			</div>
		</div>
	);
}

/** The PUT/POST body of the key form. An empty value is left out, so an edit keeps the stored key. */
export function keyRequestBody(f: {
	provider: ProviderId;
	label: string;
	baseUrl: string;
	modelsText: string;
	budget: string;
	value: string;
	/** Price per 1M tokens as typed; all empty or zero sends no price (and clears a stored one). */
	price?: { input: string; output: string; cache: string };
}): ModelKeyRequest {
	const price = keyPrice(f.price);
	return {
		provider: f.provider,
		label: f.label.trim(),
		base_url: f.baseUrl.trim(),
		models: parseModels(f.modelsText),
		budget_usd: Number(f.budget) || 0,
		...(f.value.trim() ? { value: f.value.trim() } : {}),
		...(price ? { price } : {}),
	};
}

/** The typed price fields as a key price, or undefined when none is a positive number. */
export function keyPrice(p: { input: string; output: string; cache: string } | undefined): ModelKeyPrice | undefined {
	if (!p) return undefined;
	const num = (v: string) => {
		const n = Number(v.trim());
		return Number.isFinite(n) && n > 0 ? n : 0;
	};
	const out = { input_per_mtok: num(p.input), output_per_mtok: num(p.output), cache_per_mtok: num(p.cache) };
	return out.input_per_mtok || out.output_per_mtok || out.cache_per_mtok ? out : undefined;
}

/**
 * The key value for a provider whose credential is several fields (Bedrock, Vertex, Cloudflare,
 * Azure...): a JSON object of the filled fields. "" when none is filled.
 */
export function credentialValue(fields: Record<string, string>): string {
	const filled = Object.fromEntries(
		Object.entries(fields)
			.filter(([, v]) => v.trim() !== "")
			.map(([k, v]) => [k, v.trim()]),
	);
	return Object.keys(filled).length ? JSON.stringify(filled) : "";
}

/** The required credential fields still empty, by name. */
export function missingCredentialFields(spec: Pick<BifrostProviderSpec, "fields"> | undefined, fields: Record<string, string>): string[] {
	return (spec?.fields ?? []).filter((f) => f.required && !(fields[f.name] ?? "").trim()).map((f) => f.name);
}

/** Attributes that keep browsers and password managers from filling a field. */
export const NO_AUTOFILL = {
	"data-1p-ignore": "true",
	"data-lpignore": "true",
	"data-form-type": "other",
} as const;

function KeyForm({
	providers,
	initial,
	onDone,
}: {
	providers: ModelProviderInfo[];
	initial: { provider: ProviderId; key?: ModelKey };
	onDone(): void;
}) {
	const { t } = useTranslation("models");
	const id = useId();
	const editing = initial.key;
	const [provider, setProvider] = useState<ProviderId>(initial.provider);
	const [label, setLabel] = useState(editing?.label ?? "");
	const [value, setValue] = useState("");
	const [baseUrl, setBaseUrl] = useState(editing?.base_url ?? "");
	const [modelsText, setModelsText] = useState((editing?.models ?? []).join(", "));
	const [budget, setBudget] = useState(String(editing?.budget_usd ?? 0));
	const [priceInput, setPriceInput] = useState(editing?.price?.input_per_mtok ? String(editing.price.input_per_mtok) : "");
	const [priceOutput, setPriceOutput] = useState(editing?.price?.output_per_mtok ? String(editing.price.output_per_mtok) : "");
	const [priceCache, setPriceCache] = useState(editing?.price?.cache_per_mtok ? String(editing.price.cache_per_mtok) : "");
	const [touched, setTouched] = useState(false);
	const [create, createState] = useCreateModelKeyMutation();
	const [update, updateState] = useUpdateModelKeyMutation();
	const info = providers.find((p) => p.id === provider);
	// GET /models/providers (A09): credential fields, base URL need, keyless servers.
	const specs = useGetModelProvidersQuery();
	const spec = specs.data?.providers?.find((p) => p.id === provider);
	const credFields = spec?.fields ?? [];
	const [fieldValues, setFieldValues] = useState<Record<string, string>>({});
	const effectiveValue = credFields.length ? credentialValue(fieldValues) : value;
	const missingFields = missingCredentialFields(spec, fieldValues);
	const valueMissing = !editing && !spec?.keyless && (credFields.length ? missingFields.length > 0 || !effectiveValue : !value.trim());
	const saving = createState.isLoading || updateState.isLoading;
	const error = createState.error ?? updateState.error;

	const valueError = touched && valueMissing ? t("valueRequired") : undefined;
	const urlRequired = needsBaseUrl(info) || Boolean(spec?.needs_base_url);
	const urlError = touched && urlRequired && !baseUrl.trim() ? t("baseUrlRequired") : undefined;
	const formProviders: Pick<ModelProviderInfo, "id" | "name" | "custom_base_url">[] = providers.length
		? providers
		: PROVIDERS.map((p) => ({ id: p, name: providerName(p, []) }));

	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		setTouched(true);
		if (valueMissing || (urlRequired && !baseUrl.trim())) return;
		const body = keyRequestBody({
			provider,
			label,
			baseUrl,
			modelsText,
			budget,
			value: effectiveValue,
			price: { input: priceInput, output: priceOutput, cache: priceCache },
		});
		try {
			if (editing) await update({ id: editing.id, body }).unwrap();
			else await create(body).unwrap();
			setValue("");
			setFieldValues({});
			onDone();
		} catch {
			// error renders below
		}
	};

	const field = "flex min-w-0 flex-col gap-1.5";
	const lbl = "text-[13px] font-medium";
	return (
		<form className="flex flex-col gap-5" autoComplete="off" onSubmit={(e) => void onSubmit(e)} noValidate data-testid="model-key-form">
			<div className={field}>
				<label className={lbl} htmlFor={`${id}-provider`}>
					{t("provider")}
				</label>
				<select
					id={`${id}-provider`}
					className="border-input bg-background h-9 rounded-md border px-2 text-sm"
					value={provider}
					disabled={Boolean(editing)}
					onChange={(e) => setProvider(e.target.value)}
					data-testid="model-key-provider"
				>
					{!formProviders.some((p) => p.id === provider) && <option value={provider}>{providerName(provider, providers)}</option>}
					{groupProviders(formProviders).map((g) => (
						<optgroup key={g.group} label={t(`groups.${g.group}`)}>
							{g.providers.map((p) => (
								<option key={p.id} value={p.id}>
									{p.name || providerName(p.id, providers)}
								</option>
							))}
						</optgroup>
					))}
				</select>
				{info?.key_hint && <p className="text-muted-foreground text-xs">{info.key_hint}</p>}
			</div>
			<div className={field}>
				<label className={lbl} htmlFor={`${id}-label`}>
					{t("label")}
				</label>
				<Input
					id={`${id}-label`}
					name="heimdall-key-label"
					autoComplete="off"
					{...NO_AUTOFILL}
					value={label}
					placeholder={t("labelPlaceholder")}
					onChange={(e) => setLabel(e.target.value)}
					data-testid="model-key-label"
				/>
			</div>
			{credFields.length > 0 ? (
				<fieldset className="flex min-w-0 flex-col gap-3" data-testid="model-key-fields">
					<legend className={lbl}>{t("value")}</legend>
					{credFields.map((f) => (
						<div key={f.name} className={field}>
							<label className="text-xs font-medium" htmlFor={`${id}-f-${f.name}`}>
								{f.label}
								{f.required ? " *" : ""}
							</label>
							<Input
								id={`${id}-f-${f.name}`}
								type={f.secret ? "password" : "text"}
								name={`heimdall-key-field-${f.name}`}
								autoComplete={f.secret ? "new-password" : "off"}
								{...NO_AUTOFILL}
								value={fieldValues[f.name] ?? ""}
								placeholder={editing && f.secret ? t("valueKeep") : ""}
								onChange={(e) => setFieldValues((v) => ({ ...v, [f.name]: e.target.value }))}
								data-testid={`model-key-field-${f.name}`}
							/>
							{f.help && <p className="text-muted-foreground text-xs">{f.help}</p>}
						</div>
					))}
					{valueError && <p className="text-chart-error-ink text-xs">{valueError}</p>}
				</fieldset>
			) : (
				<div className={field}>
					<label className={lbl} htmlFor={`${id}-value`}>
						{t("value")}
					</label>
					<Input
						id={`${id}-value`}
						type="password"
						name="heimdall-key-value"
						autoComplete="new-password"
						{...NO_AUTOFILL}
						value={value}
						placeholder={editing ? t("valueKeep") : (info?.key_prefix ?? "") + "..."}
						onChange={(e) => setValue(e.target.value)}
						aria-invalid={Boolean(valueError) || undefined}
						data-testid="model-key-value"
					/>
					{valueError ? (
						<p className="text-chart-error-ink text-xs">{valueError}</p>
					) : editing ? (
						<p className="text-muted-foreground text-xs" data-testid="model-key-value-keep">
							{t("valueKeepHint")}
						</p>
					) : (
						providerGroup({ id: provider, custom_base_url: info?.custom_base_url }) === "self-hosted" && (
							<p className="text-muted-foreground text-xs">{t("selfHostedKeyHint")}</p>
						)
					)}
				</div>
			)}
			<div className={field}>
				<label className={lbl} htmlFor={`${id}-url`}>
					{t("baseUrl")}
				</label>
				<Input
					id={`${id}-url`}
					name="heimdall-key-base-url"
					autoComplete="off"
					{...NO_AUTOFILL}
					value={baseUrl}
					placeholder={info?.default_base_url ?? "https://api.example.com/v1"}
					onChange={(e) => setBaseUrl(e.target.value)}
					aria-invalid={Boolean(urlError) || undefined}
					data-testid="model-key-base-url"
				/>
				<p className="text-muted-foreground text-xs">{urlError ?? (urlRequired ? t("baseUrlRequired") : t("baseUrlOptional"))}</p>
			</div>
			<div className={field}>
				<label className={lbl} htmlFor={`${id}-models`}>
					{t("models")}
				</label>
				<Input
					id={`${id}-models`}
					name="heimdall-key-models"
					autoComplete="off"
					{...NO_AUTOFILL}
					value={modelsText}
					placeholder={(info?.models ?? [])
						.map((m) => m.id)
						.slice(0, 3)
						.join(", ")}
					onChange={(e) => setModelsText(e.target.value)}
					data-testid="model-key-models"
				/>
				<p className="text-muted-foreground text-xs">{t("modelsHint")}</p>
			</div>
			<div className={field}>
				<label className={lbl} htmlFor={`${id}-budget`}>
					{t("budget")}
				</label>
				<Input
					id={`${id}-budget`}
					type="number"
					min={0}
					step="1"
					value={budget}
					onChange={(e) => setBudget(e.target.value)}
					data-testid="model-key-budget"
				/>
				<p className="text-muted-foreground text-xs">{t("budgetHint")}</p>
			</div>
			<fieldset className={field} data-testid="model-key-price">
				<legend className={cn(lbl, "mb-1.5")}>{t("price")}</legend>
				<div className="grid grid-cols-3 gap-2">
					{(
						[
							["input", t("priceInput"), priceInput, setPriceInput],
							["output", t("priceOutput"), priceOutput, setPriceOutput],
							["cache", t("priceCache"), priceCache, setPriceCache],
						] as const
					).map(([name, label, v, set]) => (
						<div key={name} className="flex min-w-0 flex-col gap-1">
							<label className="text-muted-foreground text-xs" htmlFor={`${id}-price-${name}`}>
								{label}
							</label>
							<Input
								id={`${id}-price-${name}`}
								type="number"
								min={0}
								step="any"
								inputMode="decimal"
								placeholder="0.00"
								value={v}
								onChange={(e) => set(e.target.value)}
								data-testid={`model-key-price-${name}`}
							/>
						</div>
					))}
				</div>
				<p className="text-muted-foreground text-xs">{t("priceHint")}</p>
			</fieldset>
			{error && (
				<p role="alert" className="text-chart-error-ink text-xs" data-testid="model-key-error">
					{getBenchErrorMessage(error)}
				</p>
			)}
			<div className="flex gap-2 border-t pt-4">
				<Button type="submit" size="sm" disabled={saving} data-testid="model-key-save">
					{saving ? t("saving") : t("save")}
				</Button>
				<Button type="button" size="sm" variant="outline" onClick={onDone}>
					{t("cancel")}
				</Button>
			</div>
		</form>
	);
}