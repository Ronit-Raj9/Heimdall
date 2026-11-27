import "@/components/settings/uf8aI18n";
import "@/components/gateway/ufc7I18n";
import "@/components/settings/ufrkI18n";
import { useListEvaluationsLightQuery } from "@/lib/store/apis/evaluationsApi";
import { notify } from "@/lib/notify";
import { PROVIDERS, type ProviderId } from "@/components/models/modelPicker";
import { CredentialKinds } from "@/components/settings/credentialKinds";
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
import { BlockSkeleton, InlineError, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { DetailsDisclosure } from "@/components/ui/detailsDisclosure";
import { Combobox } from "@/components/ui/combobox";
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
	ModelPriceView,
	ModelKeyRequest,
	ModelKeyTestResult,
	ModelProviderInfo,
} from "@/lib/types/models";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { useEmbedded } from "@/components/shell/embedded";
import { cn } from "@/lib/utils";
import { KeyRound, Pencil, Plus, Sparkles, X } from "lucide-react";
import { useId, useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

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

/**
 * The in-app path named by ?return=<path> (where the user came from, such as the evaluation
 * wizard), or null. Only same-site paths count: a full URL or "//host" is ignored.
 */
export function returnParam(search: unknown): string | null {
	// Q-309: the wizard may send ?returnTo=; older links use ?return=.
	const sp = search as { return?: unknown; returnTo?: unknown } | undefined;
	const ret = sp?.return ?? sp?.returnTo;
	if (typeof ret !== "string" || !ret.startsWith("/") || ret.startsWith("//") || ret.startsWith("/\\")) return null;
	return ret;
}

/** The ?filter=<text> of the provider list, "" when absent. */
export function filterParam(search: unknown): string {
	const f = (search as { filter?: unknown } | undefined)?.filter;
	return typeof f === "string" ? f : "";
}

/**
 * A provider hint such as "Create a key at console.anthropic.com/settings/keys" split into text
 * and the address in it, so the address can render as a link (https added when missing).
 */
export function hintParts(text: string): { text: string; href?: string }[] {
	const m = /((?:https?:\/\/)?[a-z0-9-]+(?:\.[a-z0-9-]+)+\/[^\s)]*[^\s).,])/i.exec(text);
	if (!m) return [{ text }];
	const url = m[1];
	const parts: { text: string; href?: string }[] = [];
	if (m.index > 0) parts.push({ text: text.slice(0, m.index) });
	parts.push({ text: url, href: /^https?:\/\//i.test(url) ? url : `https://${url}` });
	const rest = text.slice(m.index + url.length);
	if (rest) parts.push({ text: rest });
	return parts;
}

/** A typed amount that must be 0 or more: true when it is a negative number. */
export function isNegative(v: string): boolean {
	const n = Number(v.trim());
	return v.trim() !== "" && Number.isFinite(n) && n < 0;
}

/** The model list text with one more model, once. Pure. */
export function addModel(text: string, model: string): string {
	const list = parseModels(text);
	return (list.includes(model) ? list : [...list, model]).join(", ");
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

/** A price per 1M tokens: two decimals, more only when cents would hide it ($0.075). */
export function formatPrice(v: number): string {
	const cents = Math.round(v * 100) / 100;
	return Math.abs(cents - v) < 1e-9 ? formatUSD(v) : `$${Number(v.toFixed(4))}`;
}

/** The default model's price line: from a key, from the price list, or not set. */
export function defaultPriceText(t: TFunction<"models">, price: ModelPriceView | undefined): string {
	if (!price) return t("defaultPrice.none");
	const vals = {
		input: formatPrice(price.input_per_mtok),
		output: formatPrice(price.output_per_mtok),
		cache: formatPrice(price.cache_per_mtok || price.input_per_mtok),
	};
	return price.source === "key" ? t("defaultPrice.key", { ...vals, label: price.key_label ?? "" }) : t("defaultPrice.priceList", vals);
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

/** The provider choices of the key form, grouped as the catalog groups them; a stored key's unknown provider stays listed. Pure, exported for tests. */
export function providerItems(
	formProviders: Pick<ModelProviderInfo, "id" | "name" | "custom_base_url">[],
	current: ProviderId,
	all: ModelProviderInfo[],
	groupName: (g: ProviderGroup) => string,
): { value: string; label: string; group?: string }[] {
	const out: { value: string; label: string; group?: string }[] = [];
	if (!formProviders.some((p) => p.id === current)) out.push({ value: current, label: providerName(current, all) });
	for (const g of groupProviders(formProviders)) {
		for (const p of g.providers) out.push({ value: p.id, label: p.name || providerName(p.id, all), group: groupName(g.group) });
	}
	return out;
}

/** Model keys: your keys first (add, edit, test, delete), the default model, then every provider behind "Browse providers". */
export default function ModelsView() {
	const { t } = useTranslation("models");
	const { t: tu } = useTranslation("u11");
	const { t: tl } = useTranslation("uf8a");
	const { t: tc } = useTranslation("ufc7");
	const session = useSession();
	const search = useSearch({ strict: false });
	const navigate = useNavigate();
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
	const returnTo = returnParam(search);
	const [saved, setSaved] = useState(false);
	// Q-286: the panel was closed without saving; a person sent here from the wizard still gets the link back.
	const [closed, setClosed] = useState(false);
	const { t: tr } = useTranslation("ufrk");
	// Q-309: with no evaluation yet, offer the way back to the set-up steps on Home.
	const evals = useListEvaluationsLightQuery({ limit: 1, status: "" }, { skip: Boolean(returnTo) });
	const noEvaluationYet = !returnTo && evals.isSuccess && (evals.data?.length ?? 0) === 0;
	// The provider filter lives in the URL (?filter=), so a shared link keeps it.
	const filter = filterParam(search);
	const setFilter = (v: string) =>
		void navigate({ search: ((prev: Record<string, unknown>) => ({ ...prev, filter: v || undefined })) as never, replace: true });
	const [form, setForm] = useState<{
		provider: ProviderId;
		key?: ModelKey;
	} | null>(initialAdd && isAdmin ? { provider: initialAdd } : null);

	const listed: ModelProviderInfo[] = providers.length
		? providers
		: PROVIDERS.map((id) => ({ id, name: providerName(id, []), needs_base_url: id === "openai-compatible", key_hint: "", models: [] }));
	const groups = groupProviders(filterProviders(listed, filter, (g) => t(`groups.${g}`)));
	const firstProvider: ProviderId = listed[0]?.id ?? "openai";
	const keyed = listed.filter((p) => keys.some((k) => k.provider === p.id));
	// Keys of a provider the catalog does not list still show, under their own id.
	const orphan = [...new Set(keys.map((k) => k.provider))].filter((id) => !listed.some((p) => p.id === id));

	return (
		<div className={cn("mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-8", !embedded && "p-4 sm:p-6")} data-testid="models-view">
			<header className="flex flex-wrap items-start justify-between gap-4">
				<div className="flex min-w-0 flex-col gap-1">
					<Heading className={embedded ? "text-xl font-semibold tracking-tight" : "text-2xl font-semibold tracking-tight"}>
						{tl("settings.tabKeys")}
					</Heading>
					<p className="text-muted-foreground text-sm" data-testid="models-intro">
						{tl("kinds.keys.what")}
					</p>
					<p className="text-muted-foreground text-xs">
						{tl("models.secretsHint")}{" "}
						<Link
							to="/settings"
							search={{ tab: "secrets" } as never}
							className="text-foreground inline-flex min-h-6 items-center underline-offset-4 hover:underline"
							data-testid="models-secrets-link"
						>
							{t("secretsLink")}
						</Link>
					</p>
				</div>
				{isAdmin && models.data?.keys_ready && (
					<Button size="sm" className="gap-1.5 max-sm:w-full" onClick={() => setForm({ provider: firstProvider })} data-testid="models-add">
						<Plus className="size-4" aria-hidden="true" />
						{tu("models.addKey")}
					</Button>
				)}
			</header>
			<CredentialKinds current="keys" />

			{returnTo && (saved || closed) && !form && (
				<div
					role="status"
					className={cn(
						"flex flex-wrap items-center justify-between gap-3 rounded-xl border px-5 py-3",
						saved ? "border-chart-success/40 bg-chart-success/10" : "bg-card",
					)}
					data-testid="models-return"
				>
					<span className="text-sm font-medium">{saved ? tl("models.returnSaved") : tr("models.returnCancelled")}</span>
					<Button asChild size="sm">
						<a href={returnTo}>{tl("models.returnAction")}</a>
					</Button>
				</div>
			)}

			{noEvaluationYet && keys.length > 0 && !form && (
				<div className="bg-card flex flex-wrap items-center justify-between gap-3 rounded-xl border px-5 py-3" data-testid="models-setup">
					<span className="text-sm">{tr("models.setupLine")}</span>
					<Button asChild size="sm" variant="outline">
						<Link to="/">{tr("models.setupAction")}</Link>
					</Button>
				</div>
			)}

			{models.isLoading ? (
				<BlockSkeleton rows={4} testId="models-loading" />
			) : models.isError || !models.data ? (
				<LoadError title={t("loadError")} error={models.error} onRetry={() => void models.refetch()} testId="models-error" />
			) : !models.data.keys_ready ? (
				<EmptyState
					icon={KeyRound}
					message={`${isAdmin ? tl("models.unavailableAdmin") : tl("models.unavailableViewer")} ${tr("models.freeCheck")}`}
					action={
						isAdmin
							? { label: t("unavailableAction"), to: "/docs", search: { doc: "hosting" } }
							: { label: tl("models.whoRuns"), to: "/settings", search: { tab: "members" } }
					}
					testId="models-unavailable"
				/>
			) : (
				<>
					{keys.length === 0 ? (
						<EmptyState
							icon={KeyRound}
							message={isAdmin ? t("empty") : t("emptyViewer")}
							action={
								isAdmin
									? {
											label: tc("models.add"),
											onClick: () => setForm({ provider: firstProvider }),
										}
									: { label: t("emptyViewerAction"), to: "/experiments" }
							}
							testId="models-empty"
						/>
					) : (
						<section className="flex flex-col gap-3" data-testid="models-yours">
							<h3 className="text-lg font-semibold">{tl("models.yourKeys")}</h3>
							<div className="grid gap-4 lg:grid-cols-2">
								{keyed.map((p) => (
									<ProviderCard
										key={p.id}
										provider={p.id}
										name={p.name || providerName(p.id, providers)}
										group={providerGroup(p)}
										keys={keys.filter((k) => k.provider === p.id)}
										modelIds={p.models.map((m) => m.id)}
										isAdmin={isAdmin}
										canTest={canTest}
										onAdd={() => setForm({ provider: p.id })}
										onEdit={(k) => setForm({ provider: k.provider, key: k })}
									/>
								))}
								{orphan.map((id) => (
									<ProviderCard
										key={id}
										provider={id}
										name={providerName(id, providers)}
										group="other"
										keys={keys.filter((k) => k.provider === id)}
										isAdmin={isAdmin}
										canTest={canTest}
										onAdd={() => setForm({ provider: id })}
										onEdit={(k) => setForm({ provider: k.provider, key: k })}
									/>
								))}
							</div>
						</section>
					)}
					<div className="bg-card flex flex-wrap items-center gap-3 rounded-xl border px-5 py-4" data-testid="models-default">
						<span className="bg-primary/10 text-primary flex size-8 items-center justify-center rounded-lg">
							<Sparkles className="size-4" aria-hidden="true" />
						</span>
						{models.data.default ? (
							<div className="flex min-w-0 flex-col">
								<span className="text-muted-foreground text-xs">{tu("models.defaultLabel")}</span>
								<span className="text-sm font-medium break-words">
									{models.data.default.model}
									<span className="text-muted-foreground font-normal"> · {providerName(models.data.default.provider, providers)}</span>
								</span>
							</div>
						) : (
							<span className="text-muted-foreground text-sm">{t("noDefault")}</span>
						)}
						<span
							className="text-muted-foreground text-xs underline decoration-dotted underline-offset-4 sm:ml-auto"
							title={t("priceHintTitle")}
							data-testid="models-price-hint"
						>
							{defaultPriceText(t, models.data.default?.price)}
						</span>
					</div>
					<DetailsDisclosure
						label={tl("models.browse")}
						meta={tl("models.browseMeta", { count: listed.length })}
						defaultOpen={keys.length === 0 || Boolean(filter)}
						testId="models-browse"
					>
						<div className="flex flex-col gap-6" data-testid="models-table">
							<div className="flex flex-wrap items-center justify-between gap-3">
								<p className="text-muted-foreground text-sm">{t("providersTitle")}</p>
								<Input
									type="search"
									autoComplete="off"
									className="h-8 w-full sm:w-56"
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
									<div className="grid gap-4 lg:grid-cols-2">
										{g.providers.map((p) => (
											<ProviderCard
												key={p.id}
												provider={p.id}
												name={p.name || providerName(p.id, providers)}
												group={g.group}
												keys={keys.filter((k) => k.provider === p.id)}
												isAdmin={isAdmin}
												canTest={canTest}
												compact
												onAdd={() => setForm({ provider: p.id })}
												onEdit={(k) => setForm({ provider: k.provider, key: k })}
											/>
										))}
									</div>
								</section>
							))}
							<OpenModels providers={providers} keys={keys} />
						</div>
					</DetailsDisclosure>
					{form && (
						<SidePanel
							title={form.key ? t("editTitle") : tc("models.add")}
							onClose={() => {
								setForm(null);
								setClosed(true);
							}}
						>
							<KeyForm
								key={`${form.provider}-${form.key?.id ?? "new"}`}
								providers={providers}
								initial={form}
								canTest={canTest}
								onSaved={() => setSaved(true)}
								onDone={() => {
									setForm(null);
									setClosed(true);
								}}
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
				<h3 className="text-base font-semibold">{t("open.title")}</h3>
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
								<span className="text-muted-foreground truncate font-mono text-xs" title={serving.flatMap((p) => p.models).join(", ")}>
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
	compact,
	modelIds = [],
	onAdd,
	onEdit,
}: {
	provider: ProviderId;
	name: string;
	group: ProviderGroup;
	keys: ModelKey[];
	isAdmin: boolean;
	canTest: boolean;
	/** Browse list: header only, the keys show under "Your keys". */
	compact?: boolean;
	/** The provider's models in the price list, for "All N models" on a key with no model list. */
	modelIds?: string[];
	onAdd(): void;
	onEdit(k: ModelKey): void;
}) {
	const { t } = useTranslation("models");
	const { t: tu } = useTranslation("u11");
	const { t: tl } = useTranslation("uf8a");
	const { t: tc } = useTranslation("ufc7");
	const state = providerKeyState(keys);
	return (
		<section
			className={cn("bg-card flex min-w-0 flex-col gap-4 rounded-xl border", compact ? "p-4" : "p-5")}
			data-testid={`provider-card-${provider}`}
		>
			<div className="flex flex-wrap items-center gap-x-3 gap-y-2">
				<ProviderTile provider={provider} group={group} />
				<div className="flex min-w-0 flex-1 flex-col">
					<h4 className="line-clamp-2 text-base font-semibold break-words">{name}</h4>
					<span className="text-muted-foreground text-xs">
						{keys.length ? tu("models.keysCount", { count: keys.length }) : tu("models.noKeys")}
					</span>
				</div>
				{/* "No keys yet" already says it; the badge shows only a real state. */}
				{state !== "none" && (
					<Badge variant={PROVIDER_STATUS_VARIANT[state]} className="rounded-full px-2 text-xs" data-testid={`provider-status-${provider}`}>
						{t(`providerStatus.${state}`)}
					</Badge>
				)}
				{isAdmin && (
					<Button
						size="sm"
						variant="ghost"
						className="gap-1 max-sm:w-full max-sm:justify-start"
						onClick={onAdd}
						aria-label={tc("models.addKeyFor", { provider: name })}
						data-testid={`provider-add-${provider}`}
					>
						<Plus className="size-3.5" aria-hidden="true" />
						{tu("models.addKey")}
					</Button>
				)}
			</div>
			{!compact && keys.length > 0 && (
				<ul className="flex flex-col divide-y rounded-lg border">
					{keys.map((k) => (
						<KeyRow
							key={k.id}
							k={k}
							providerLabel={name}
							modelIds={modelIds}
							isAdmin={isAdmin}
							canTest={canTest}
							onEdit={() => onEdit(k)}
						/>
					))}
				</ul>
			)}
		</section>
	);
}

/** A key's test result, kept per key so the form's "Test key" and the row's Test read the same way. */
function useKeyTest(id: string | undefined) {
	const [test, testState] = useTestModelKeyMutation();
	const [result, setResult] = useState<ModelKeyTestResult | null>(null);
	const run = async () => {
		if (!id) return;
		try {
			const r = await test(id).unwrap();
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
	return { run, result, testing: testState.isLoading };
}

function TestResult({ result, testId }: { result: ModelKeyTestResult; testId: string }) {
	return (
		<p
			role={result.ok ? "status" : "alert"}
			className={`text-xs ${result.ok ? "text-chart-success-ink" : "text-chart-error-ink"}`}
			data-testid={testId}
		>
			{result.message}
		</p>
	);
}

function KeyRow({
	k,
	providerLabel,
	modelIds,
	isAdmin,
	canTest,
	onEdit,
}: {
	k: ModelKey;
	providerLabel: string;
	modelIds: string[];
	isAdmin: boolean;
	canTest: boolean;
	onEdit(): void;
}) {
	const { t } = useTranslation("models");
	const { t: tl } = useTranslation("uf8a");
	const [remove, removeState] = useDeleteModelKeyMutation();
	const keyTest = useKeyTest(k.id);
	const name = k.label || providerLabel || k.provider;
	const pct = spendPercent(k.month_spent_usd, k.budget_usd);
	const variant = STATUS_VARIANT[k.status] ?? "outline";
	const price = k.price
		? t("priceShown", {
				input: formatUSD(k.price.input_per_mtok),
				output: formatUSD(k.price.output_per_mtok),
				cache: formatUSD(k.price.cache_per_mtok || k.price.input_per_mtok),
			})
		: "";
	return (
		<li className="flex min-w-0 flex-col gap-3 p-4" data-testid={`model-key-${k.id}`}>
			<div className="flex flex-wrap items-center gap-2">
				<span className="min-w-0 truncate text-sm font-medium" title={name}>
					{name}
				</span>
				<span className="text-muted-foreground font-mono text-xs">{k.last4 ? `...${k.last4}` : ""}</span>
				<Badge variant={variant} className="rounded-full px-2 text-xs" data-testid={`model-key-status-${k.id}`}>
					<span className={cn("size-1.5 rounded-full", STATUS_DOT[k.status] ?? STATUS_DOT.untested)} aria-hidden="true" />
					{t(`status.${k.status}`)}
				</Badge>
				{k.over_budget && (
					<Badge variant="destructive" className="rounded-full px-2 text-xs">
						{t("overBudget")}
					</Badge>
				)}
				<div className="flex flex-wrap items-center gap-1 sm:ml-auto">
					{canTest && (
						<Button
							size="sm"
							variant="outline"
							className="h-9 px-2 text-xs sm:h-7"
							disabled={keyTest.testing}
							onClick={() => void keyTest.run()}
							aria-label={tl("models.testFor", { name })}
							title={tl("models.testTitle")}
							data-testid={`model-key-test-${k.id}`}
						>
							{keyTest.testing ? t("testing") : tl("models.test")}
						</Button>
					)}
					{isAdmin && (
						<>
							<Button
								size="sm"
								variant="ghost"
								className="h-9 px-2 sm:h-7"
								onClick={onEdit}
								aria-label={tl("models.editFor", { name })}
								title={t("edit")}
								data-testid={`model-key-edit-${k.id}`}
							>
								<Pencil className="size-3.5" aria-hidden="true" />
							</Button>
							<ConfirmButton
								label={t("delete")}
								question={t("confirmDelete", { label: name })}
								confirmLabel={t("delete")}
								cancelLabel={t("cancel")}
								onConfirm={() =>
									void remove(k.id)
										.unwrap()
										.then(() => notify({ text: t("deleted", { ns: "toast", name }) }))
										.catch(() => undefined)
								}
								isLoading={removeState.isLoading}
								testId={`model-key-delete-${k.id}`}
							/>
							<InlineError error={removeState.error} title={tl("models.deleteError")} testId={`model-key-delete-error-${k.id}`} className="basis-full" />
						</>
					)}
				</div>
			</div>
			<div className="flex flex-col gap-1.5">
				<div className="text-muted-foreground flex flex-wrap items-center justify-between gap-x-2 gap-y-1 text-xs">
					<span className="min-w-0 truncate" title={(k.models.length ? k.models : modelIds).join(", ") || undefined}>
						{k.models.length
							? k.models.join(", ")
							: modelIds.length
								? tl("models.allModels", { count: modelIds.length, provider: providerLabel || k.provider })
								: tl("models.anyModel", { provider: providerLabel || k.provider })}
					</span>
					<span className="shrink-0 tabular-nums" title={price || undefined} data-testid={`model-key-spent-${k.id}`}>
						{k.budget_usd > 0
							? tl("models.spent", {
									spent: formatUSD(k.month_spent_usd),
									budget: formatUSD(k.budget_usd),
								})
							: tl("models.spentNoLimit", {
									spent: formatUSD(k.month_spent_usd),
								})}
					</span>
				</div>
				{price && (
					<span className="text-muted-foreground text-xs tabular-nums" data-testid={`model-key-price-${k.id}`}>
						{price}
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
			{keyTest.result && <TestResult result={keyTest.result} testId={`model-key-result-${k.id}`} />}
			{removeState.error && (
				<p role="alert" className="text-chart-error-ink text-xs">
					{getBenchErrorMessage(removeState.error)}
				</p>
			)}
		</li>
	);
}

/** A right-hand side panel over the page (the shared Dialog drawer); Escape or the backdrop closes it. */
function SidePanel({ title, onClose, children }: { title: string; onClose(): void; children: ReactNode }) {
	const { t: tu } = useTranslation("u11");
	return (
		<Dialog
			open
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
			side="right"
			title={title}
			closeLabel={tu("models.close")}
			testId="model-key-panel"
		>
			{children}
		</Dialog>
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
	canTest,
	onSaved,
	onDone,
}: {
	providers: ModelProviderInfo[];
	initial: { provider: ProviderId; key?: ModelKey };
	canTest: boolean;
	/** Called once a key is stored (create or edit), before the panel offers the test. */
	onSaved(): void;
	onDone(): void;
}) {
	const { t } = useTranslation("models");
	const { t: tl } = useTranslation("uf8a");
	const { t: tc } = useTranslation("ufc7");
	const { t: tr } = useTranslation("ufrk");
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
	// After Save the panel stays open on the stored key and offers the test (free for most providers).
	const [stored, setStored] = useState<ModelKey | null>(null);
	const [create, createState] = useCreateModelKeyMutation();
	const [update, updateState] = useUpdateModelKeyMutation();
	const [remove, removeState] = useDeleteModelKeyMutation();
	const keyTest = useKeyTest(stored?.id);
	const info = providers.find((p) => p.id === provider);
	const providerLabel = info?.name || providerName(provider, providers);
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
	const budgetError = isNegative(budget) ? tl("models.mustBeZeroOrMore") : undefined;
	const priceError = [priceInput, priceOutput, priceCache].some(isNegative) ? tl("models.mustBeZeroOrMore") : undefined;
	const formProviders: Pick<ModelProviderInfo, "id" | "name" | "custom_base_url">[] = providers.length
		? providers
		: PROVIDERS.map((p) => ({ id: p, name: providerName(p, []) }));
	const modelIds = (info?.models ?? []).map((m) => m.id);

	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		setTouched(true);
		if (valueMissing || (urlRequired && !baseUrl.trim()) || budgetError || priceError) return;
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
			const r = editing ? await update({ id: editing.id, body }).unwrap() : await create(body).unwrap();
			setValue("");
			setFieldValues({});
			onSaved();
			// The test waits for its own press (it asks the provider for its model list); without a stored key back, close.
			if (r?.key && canTest) setStored(r.key);
			else onDone();
		} catch {
			// error renders below
		}
	};

	if (stored) {
		const failed = keyTest.result && !keyTest.result.ok;
		return (
			<div className="flex flex-col gap-4" data-testid="model-key-saved">
				<p role="status" className="text-sm font-medium">
					{tl("models.savedLine", { name: stored.label || providerLabel })}
				</p>
				<p className="text-muted-foreground text-xs">{tl("models.testTitle")}</p>
				{keyTest.result && <TestResult result={keyTest.result} testId="model-key-saved-result" />}
				{removeState.error && (
					<p role="alert" className="text-chart-error-ink text-xs">
						{getBenchErrorMessage(removeState.error)}
					</p>
				)}
				<div className="flex flex-wrap gap-2 border-t pt-4">
					<Button
						size="sm"
						variant="secondary"
						disabled={keyTest.testing}
						onClick={() => void keyTest.run()}
						data-testid="model-key-saved-test"
					>
						{keyTest.testing ? t("testing") : tl("models.testKey")}
					</Button>
					{failed && (
						<>
							<Button size="sm" variant="outline" onClick={() => setStored(null)} data-testid="model-key-saved-edit">
								{t("edit")}
							</Button>
							<Button
								size="sm"
								variant="ghost"
								className="text-chart-error-ink"
								disabled={removeState.isLoading}
								onClick={() =>
									void remove(stored.id)
										.unwrap()
										.then(onDone, () => undefined)
								}
								data-testid="model-key-saved-delete"
							>
								{t("delete")}
							</Button>
						</>
					)}
					<Button size="sm" onClick={onDone} data-testid="model-key-saved-done">
						{tl("models.done")}
					</Button>
				</div>
			</div>
		);
	}

	const field = "flex min-w-0 flex-col gap-1.5";
	const lbl = "text-[13px] font-medium";
	const optional = <span className="text-muted-foreground font-normal"> {tl("models.optional")}</span>;
	const baseUrlField = (
		<div className={field}>
			<label className={lbl} htmlFor={`${id}-url`}>
				{t("baseUrl")}
				{urlRequired ? " *" : optional}
			</label>
			<Input
				id={`${id}-url`}
				name="heimdall-key-base-url"
				autoComplete="off"
				{...NO_AUTOFILL}
				value={baseUrl}
				required={urlRequired}
				aria-required={urlRequired || undefined}
				placeholder={info?.default_base_url ?? "https://api.example.com/v1"}
				onChange={(e) => setBaseUrl(e.target.value)}
				aria-invalid={Boolean(urlError) || undefined}
				data-testid="model-key-base-url"
			/>
			<p className={cn("text-xs", urlError ? "text-chart-error-ink" : "text-muted-foreground")}>
				{urlError ?? (urlRequired ? t("baseUrlRequired") : t("baseUrlOptional"))}
			</p>
		</div>
	);
	return (
		<form className="flex flex-col gap-5" autoComplete="off" onSubmit={(e) => void onSubmit(e)} noValidate data-testid="model-key-form">
			<div className="bg-muted/40 flex flex-col gap-1 rounded-lg border px-3 py-2.5 text-xs" data-testid="model-key-where">
				<p>{tr("models.whereKeyGoes")}</p>
				{canTest && <p>{tr("models.testAfterSave")}</p>}
				<Link
					to="/docs"
					search={{ doc: "web-keys-secrets" } as never}
					className="text-foreground w-fit underline underline-offset-4"
					data-testid="model-key-where-doc"
				>
					{tr("models.whereDoc")}
				</Link>
			</div>
			<div className={field}>
				<Combobox
					label={`${t("provider")} *`}
					items={providerItems(formProviders, provider, providers, (g) => t(`groups.${g}`))}
					value={provider}
					disabled={Boolean(editing)}
					onChange={(v) => v && setProvider(v)}
					showTests
					testId="model-key-provider"
				/>
				{info?.key_hint && (
					<p className="text-muted-foreground text-xs" data-testid="model-key-hint">
						{hintParts(info.key_hint).map((part, i) =>
							part.href ? (
								<a key={i} href={part.href} target="_blank" rel="noreferrer" className="text-foreground underline underline-offset-4">
									{part.text}
								</a>
							) : (
								<span key={i}>{part.text}</span>
							),
						)}
					</p>
				)}
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
								required={Boolean(f.required) && !editing}
								aria-required={(Boolean(f.required) && !editing) || undefined}
								placeholder={editing && f.secret ? t("valueKeep") : ""}
								onChange={(e) => setFieldValues((v) => ({ ...v, [f.name]: e.target.value }))}
								data-testid={`model-key-field-${f.name}`}
							/>
							{f.help && <p className="text-muted-foreground text-xs">{f.help}</p>}
						</div>
					))}
					{valueError && <p className="text-chart-error-ink text-xs">{valueError}</p>}
					<p className="text-muted-foreground text-xs" data-testid="model-key-storage">
						{tl("models.storageHint")}
					</p>
				</fieldset>
			) : (
				<div className={field}>
					<label className={lbl} htmlFor={`${id}-value`}>
						{t("value")}
						{!editing && !spec?.keyless ? " *" : ""}
					</label>
					<Input
						id={`${id}-value`}
						type="password"
						name="heimdall-key-value"
						autoComplete="new-password"
						{...NO_AUTOFILL}
						value={value}
						required={!editing && !spec?.keyless}
						aria-required={(!editing && !spec?.keyless) || undefined}
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
					<p className="text-muted-foreground text-xs" data-testid="model-key-storage">
						{tl("models.storageHint")}
					</p>
				</div>
			)}
			<p className="text-muted-foreground text-xs" data-testid="model-key-unlocks">
				{modelIds.length
					? tl("models.unlocks", { provider: providerLabel, count: modelIds.length, first3: modelIds.slice(0, 3).join(", ") })
					: tl("models.unlocksAny", { provider: providerLabel })}
			</p>
			{urlRequired && baseUrlField}
			<DetailsDisclosure
				label={tl("models.moreOptions")}
				defaultOpen={Boolean(editing)}
				testId="model-key-more"
				contentClassName="flex flex-col gap-5"
			>
				<div className={field}>
					<label className={lbl} htmlFor={`${id}-label`}>
						{t("label")}
						{optional}
					</label>
					<Input
						id={`${id}-label`}
						name="heimdall-key-label"
						autoComplete="off"
						{...NO_AUTOFILL}
						value={label}
						placeholder={tl("models.labelPlaceholder", { provider: providerLabel })}
						onChange={(e) => setLabel(e.target.value)}
						data-testid="model-key-label"
					/>
				</div>
				{!urlRequired && baseUrlField}
				<div className={field}>
					{modelIds.length > 0 ? (
						<>
							<Combobox
								label={`${t("models")} ${tl("models.optional")}`}
								items={modelIds.filter((m) => !parseModels(modelsText).includes(m)).map((m) => ({ value: m, label: m }))}
								value={null}
								onChange={(v) => v && setModelsText(addModel(modelsText, v))}
								placeholder={tc("models.allowedPick")}
								testId="model-key-models"
							/>
							{parseModels(modelsText).length > 0 && (
								<ul className="flex flex-wrap gap-1.5" data-testid="model-key-models-picked">
									{parseModels(modelsText).map((m) => (
										<li key={m}>
											<Button
												type="button"
												size="sm"
												variant="outline"
												className="h-7 gap-1 px-2 font-mono text-xs"
												aria-label={tc("models.allowedRemove", { model: m })}
												onClick={() => setModelsText(parseModels(modelsText).filter((x) => x !== m).join(", "))}
											>
												{m}
												<X className="size-3" aria-hidden />
											</Button>
										</li>
									))}
								</ul>
							)}
						</>
					) : (
						<>
							<label className={lbl} htmlFor={`${id}-models`}>
								{t("models")}
								{optional}
							</label>
							<Input
								id={`${id}-models`}
								name="heimdall-key-models"
								autoComplete="off"
								{...NO_AUTOFILL}
								value={modelsText}
								onChange={(e) => setModelsText(e.target.value)}
								data-testid="model-key-models"
							/>
						</>
					)}
					<p className="text-muted-foreground text-xs">{t("modelsHint")}</p>
				</div>
				<div className={field}>
					<label className={lbl} htmlFor={`${id}-budget`}>
						{t("budget")}
						{optional}
					</label>
					<Input
						id={`${id}-budget`}
						type="number"
						min={0}
						step="0.01"
						value={budget}
						onChange={(e) => setBudget(e.target.value)}
						aria-invalid={Boolean(budgetError) || undefined}
						data-testid="model-key-budget"
					/>
					<p className={cn("text-xs", budgetError ? "text-chart-error-ink" : "text-muted-foreground")} data-testid="model-key-budget-hint">
						{budgetError ?? tl("models.budgetHint")}
					</p>
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
									aria-invalid={isNegative(v) || undefined}
									onChange={(e) => set(e.target.value)}
									data-testid={`model-key-price-${name}`}
								/>
							</div>
						))}
					</div>
					{priceError && <p className="text-chart-error-ink text-xs">{priceError}</p>}
					<p className="text-muted-foreground text-xs">{t("priceHint")}</p>
					<p className="text-muted-foreground text-xs" data-testid="model-key-price-scope">
						{t("priceNoModels")}
					</p>
				</fieldset>
			</DetailsDisclosure>
			{error && (
				<p role="alert" className="text-chart-error-ink text-xs" data-testid="model-key-error">
					{getBenchErrorMessage(error)}
				</p>
			)}
			<div className="flex gap-2 border-t pt-4">
				<Button type="submit" size="sm" disabled={saving || valueMissing} data-testid="model-key-save">
					{saving ? t("saving") : t("save")}
				</Button>
				<Button type="button" size="sm" variant="outline" onClick={onDone}>
					{t("cancel")}
				</Button>
			</div>
		</form>
	);
}