import { PROVIDERS, type ProviderId } from "@/components/models/modelPicker";
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
	useGetModelsQuery,
	useTestModelKeyMutation,
	useUpdateModelKeyMutation,
} from "@/lib/store/apis/modelsApi";
import type { ModelKey, ModelKeyRequest, ModelKeyTestResult, ModelProviderInfo } from "@/lib/types/models";
import { Link, useSearch } from "@tanstack/react-router";
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

/** The provider named by ?add=<provider>, when it is one we know. */
export function addParam(search: unknown): ProviderId | null {
	const add = (search as { add?: unknown } | undefined)?.add;
	return typeof add === "string" && (PROVIDERS as readonly string[]).includes(add) ? (add as ProviderId) : null;
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

/** Short tile text and tint per provider. */
const PROVIDER_TILE: Record<ProviderId, { initials: string; name: string; tint: string }> = {
	openai: {
		initials: "OA",
		name: "OpenAI",
		tint: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
	},
	anthropic: {
		initials: "AN",
		name: "Anthropic",
		tint: "bg-orange-500/10 text-orange-700 dark:text-orange-300",
	},
	gemini: {
		initials: "GE",
		name: "Gemini",
		tint: "bg-blue-500/10 text-blue-700 dark:text-blue-300",
	},
	openrouter: {
		initials: "OR",
		name: "OpenRouter",
		tint: "bg-violet-500/10 text-violet-700 dark:text-violet-300",
	},
	groq: {
		initials: "GQ",
		name: "Groq",
		tint: "bg-rose-500/10 text-rose-700 dark:text-rose-300",
	},
	sarvam: {
		initials: "SV",
		name: "Sarvam",
		tint: "bg-amber-500/10 text-amber-700 dark:text-amber-300",
	},
	"openai-compatible": {
		initials: "OC",
		name: "OpenAI-compatible",
		tint: "bg-slate-500/10 text-slate-700 dark:text-slate-300",
	},
};

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
	const models = useGetModelsQuery();
	const isAdmin = canUse(session, "admin");
	const canTest = canUse(session, "member");
	const initialAdd = addParam(search);
	const [form, setForm] = useState<{
		provider: ProviderId;
		key?: ModelKey;
	} | null>(initialAdd && isAdmin ? { provider: initialAdd } : null);

	const providers = models.data?.providers ?? [];
	const keys = models.data?.keys ?? [];
	const providerIds: ProviderId[] = providers.length ? (PROVIDERS.filter((p) => providers.some((x) => x.id === p)) as ProviderId[]) : [...PROVIDERS];

	return (
		<div className="mx-auto flex w-full max-w-[1200px] flex-col gap-8 p-4 sm:p-6" data-testid="models-view">
			<header className="flex flex-wrap items-start justify-between gap-4">
				<div className="flex min-w-0 flex-col gap-1">
					<h1 className="text-2xl font-semibold tracking-tight">{t("title")}</h1>
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
					<Button size="sm" className="gap-1.5" onClick={() => setForm({ provider: "openai" })} data-testid="models-add">
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
									<span className="text-muted-foreground font-normal">
										{" "}
										· {PROVIDER_TILE[models.data.default.provider as ProviderId]?.name ?? models.data.default.provider}
									</span>
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
											onClick: () => setForm({ provider: "openai" }),
										}
									: { label: t("emptyViewerAction"), to: "/experiments" }
							}
							testId="models-empty"
						/>
					)}
					<div className="grid gap-4 md:grid-cols-2" data-testid="models-table">
						{providerIds.map((p) => (
							<ProviderCard
								key={p}
								provider={p}
								name={providers.find((x) => x.id === p)?.name ?? PROVIDER_TILE[p].name}
								keys={keys.filter((k) => k.provider === p)}
								isAdmin={isAdmin}
								canTest={canTest}
								onAdd={() => setForm({ provider: p })}
								onEdit={(k) => setForm({ provider: k.provider, key: k })}
							/>
						))}
					</div>
					{form && (
						<SidePanel title={form.key ? t("editTitle") : t("addTitle")} onClose={() => setForm(null)}>
							<KeyForm key={`${form.provider}-${form.key?.id ?? "new"}`} providers={providers} initial={form} onDone={() => setForm(null)} />
						</SidePanel>
					)}
				</>
			)}
		</div>
	);
}

function ProviderTile({ provider, className }: { provider: ProviderId; className?: string }) {
	const tile = PROVIDER_TILE[provider];
	return (
		<span
			className={cn("flex size-10 shrink-0 items-center justify-center rounded-lg text-sm font-semibold tracking-tight", tile.tint, className)}
			aria-hidden="true"
		>
			{tile.initials}
		</span>
	);
}

function ProviderCard({
	provider,
	name,
	keys,
	isAdmin,
	canTest,
	onAdd,
	onEdit,
}: {
	provider: ProviderId;
	name: string;
	keys: ModelKey[];
	isAdmin: boolean;
	canTest: boolean;
	onAdd(): void;
	onEdit(k: ModelKey): void;
}) {
	const { t: tu } = useTranslation("u11");
	return (
		<section
			className="bg-card flex flex-col gap-4 rounded-xl border p-5 transition-shadow hover:shadow-md"
			data-testid={`provider-card-${provider}`}
		>
			<div className="flex items-center gap-3">
				<ProviderTile provider={provider} />
				<div className="flex min-w-0 flex-1 flex-col">
					<h2 className="truncate text-base font-semibold">{name}</h2>
					<span className="text-muted-foreground text-xs">{keys.length ? tu("models.keysCount", { count: keys.length }) : tu("models.noKeys")}</span>
				</div>
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
}): ModelKeyRequest {
	return {
		provider: f.provider,
		label: f.label.trim(),
		base_url: f.baseUrl.trim(),
		models: parseModels(f.modelsText),
		budget_usd: Number(f.budget) || 0,
		...(f.value.trim() ? { value: f.value.trim() } : {}),
	};
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
	const [touched, setTouched] = useState(false);
	const [create, createState] = useCreateModelKeyMutation();
	const [update, updateState] = useUpdateModelKeyMutation();
	const info = providers.find((p) => p.id === provider);
	const saving = createState.isLoading || updateState.isLoading;
	const error = createState.error ?? updateState.error;

	const valueError = touched && !editing && !value.trim() ? t("valueRequired") : undefined;
	const urlError = touched && info?.needs_base_url && !baseUrl.trim() ? t("baseUrlRequired") : undefined;

	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		setTouched(true);
		if ((!editing && !value.trim()) || (info?.needs_base_url && !baseUrl.trim())) return;
		const body = keyRequestBody({
			provider,
			label,
			baseUrl,
			modelsText,
			budget,
			value,
		});
		try {
			if (editing) await update({ id: editing.id, body }).unwrap();
			else await create(body).unwrap();
			setValue("");
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
					onChange={(e) => setProvider(e.target.value as ProviderId)}
					data-testid="model-key-provider"
				>
					{(providers.length ? providers.map((p) => p.id) : [...PROVIDERS]).map((p) => (
						<option key={p} value={p}>
							{providers.find((x) => x.id === p)?.name ?? p}
						</option>
					))}
				</select>
				{info && <p className="text-muted-foreground text-xs">{info.key_hint}</p>}
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
				) : (
					editing && (
						<p className="text-muted-foreground text-xs" data-testid="model-key-value-keep">
							{t("valueKeepHint")}
						</p>
					)
				)}
			</div>
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
				<p className="text-muted-foreground text-xs">{urlError ?? (info?.needs_base_url ? t("baseUrlRequired") : t("baseUrlOptional"))}</p>
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
					placeholder={info?.models
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
