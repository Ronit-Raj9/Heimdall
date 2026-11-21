import { PROVIDERS, type ProviderId } from "@/components/models/modelPicker";
import { ConfirmButton } from "@/components/platform/badges";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
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
import { useSearch } from "@tanstack/react-router";
import { KeyRound } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

const STATUS_VARIANT = { untested: "outline", ok: "success", invalid: "destructive", error: "warning" } as const;

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

/** Models & keys: the org's provider keys (add, edit, test, delete) and the default model. */
export default function ModelsView() {
	const { t } = useTranslation("models");
	const session = useSession();
	const search = useSearch({ strict: false });
	const models = useGetModelsQuery();
	const isAdmin = canUse(session, "admin");
	const canTest = canUse(session, "member");
	const initialAdd = addParam(search);
	const [form, setForm] = useState<{ provider: ProviderId; key?: ModelKey } | null>(initialAdd && isAdmin ? { provider: initialAdd } : null);

	const providers = models.data?.providers ?? [];
	const keys = models.data?.keys ?? [];

	return (
		<div className="mx-auto flex max-w-5xl flex-col gap-6 p-6" data-testid="models-view">
			<header className="flex flex-wrap items-start justify-between gap-3">
				<div>
					<h1 className="text-xl font-semibold">{t("title")}</h1>
					<p className="text-muted-foreground max-w-2xl text-sm">{t("subtitle")}</p>
				</div>
				{isAdmin && models.data?.keys_ready && !form && (
					<Button size="sm" onClick={() => setForm({ provider: "openai" })} data-testid="models-add">
						{t("add")}
					</Button>
				)}
			</header>

			{models.isLoading ? (
				<BlockSkeleton rows={4} testId="models-loading" />
			) : models.isError || !models.data ? (
				<LoadError title={t("loadError")} error={models.error} onRetry={() => void models.refetch()} testId="models-error" />
			) : !models.data.keys_ready ? (
				<EmptyState icon={KeyRound} message={t("unavailable")} testId="models-unavailable" />
			) : (
				<>
					{form && (
						<KeyForm
							providers={providers}
							initial={form}
							onDone={() => setForm(null)}
						/>
					)}
					<p className="text-sm" data-testid="models-default">
						{models.data.default
							? t("defaultModel", { model: models.data.default.model, provider: models.data.default.provider })
							: t("noDefault")}
					</p>
					{keys.length === 0 ? (
						!form && (
							<EmptyState
								icon={KeyRound}
								message={t("empty")}
								action={isAdmin ? { label: t("add"), onClick: () => setForm({ provider: "openai" }) } : undefined}
								testId="models-empty"
							/>
						)
					) : (
						<KeysTable keys={keys} isAdmin={isAdmin} canTest={canTest} onEdit={(k) => setForm({ provider: k.provider, key: k })} />
					)}
				</>
			)}
		</div>
	);
}

function KeysTable({ keys, isAdmin, canTest, onEdit }: { keys: ModelKey[]; isAdmin: boolean; canTest: boolean; onEdit(k: ModelKey): void }) {
	const { t } = useTranslation("models");
	const [test, testState] = useTestModelKeyMutation();
	const [remove, removeState] = useDeleteModelKeyMutation();
	const [results, setResults] = useState<Record<string, ModelKeyTestResult>>({});
	const runTest = async (id: string) => {
		try {
			const r = await test(id).unwrap();
			setResults((prev) => ({ ...prev, [id]: r.result }));
		} catch (err) {
			setResults((prev) => ({ ...prev, [id]: { ok: false, status: "error", message: getBenchErrorMessage(err), latency_ms: 0 } }));
		}
	};
	return (
		<div className="overflow-x-auto rounded-lg border" data-testid="models-table">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>{t("colKey")}</TableHead>
						<TableHead>{t("colStatus")}</TableHead>
						<TableHead>{t("models")}</TableHead>
						<TableHead>{t("colSpend")}</TableHead>
						<TableHead className="text-right" />
					</TableRow>
				</TableHeader>
				<TableBody>
					{keys.map((k) => {
						const result = results[k.id];
						return (
							<TableRow key={k.id} data-testid={`model-key-${k.id}`}>
								<TableCell>
									<div className="font-medium">{k.label || k.provider}</div>
									<div className="text-muted-foreground font-mono text-xs">
										{k.provider} {k.last4 ? `...${k.last4}` : ""}
										{k.base_url ? ` @ ${k.base_url}` : ""}
									</div>
								</TableCell>
								<TableCell>
									<Badge variant={STATUS_VARIANT[k.status] ?? "outline"} data-testid={`model-key-status-${k.id}`}>
										{t(`status.${k.status}`)}
									</Badge>
									{result && (
										<p
											role={result.ok ? "status" : "alert"}
											className={`mt-1 max-w-xs text-xs ${result.ok ? "text-chart-success-ink" : "text-chart-error-ink"}`}
											data-testid={`model-key-result-${k.id}`}
										>
											{result.message}
										</p>
									)}
								</TableCell>
								<TableCell className="text-muted-foreground max-w-48 truncate text-xs">{k.models.length ? k.models.join(", ") : "any"}</TableCell>
								<TableCell className="text-xs">
									{formatUSD(k.month_spent_usd)} / {k.budget_usd > 0 ? formatUSD(k.budget_usd) : t("noBudget")}
									{k.over_budget && (
										<Badge variant="destructive" className="ml-1">
											{t("overBudget")}
										</Badge>
									)}
								</TableCell>
								<TableCell className="text-right whitespace-nowrap">
									<div className="flex justify-end gap-1">
										{canTest && (
											<Button
												size="sm"
												variant="outline"
												disabled={testState.isLoading && testState.originalArgs === k.id}
												onClick={() => void runTest(k.id)}
												data-testid={`model-key-test-${k.id}`}
											>
												{testState.isLoading && testState.originalArgs === k.id ? t("testing") : t("test")}
											</Button>
										)}
										{isAdmin && (
											<>
												<Button size="sm" variant="outline" onClick={() => onEdit(k)} data-testid={`model-key-edit-${k.id}`}>
													{t("edit")}
												</Button>
												<ConfirmButton
													label={t("delete")}
													question={t("confirmDelete", { label: k.label || k.provider })}
													confirmLabel={t("delete")}
													cancelLabel={t("cancel")}
													onConfirm={() => void remove(k.id)}
													isLoading={removeState.isLoading && removeState.originalArgs === k.id}
													testId={`model-key-delete-${k.id}`}
												/>
											</>
										)}
									</div>
								</TableCell>
							</TableRow>
						);
					})}
				</TableBody>
			</Table>
			{removeState.error && (
				<p role="alert" className="text-chart-error-ink p-2 text-xs">
					{getBenchErrorMessage(removeState.error)}
				</p>
			)}
		</div>
	);
}

function KeyForm({ providers, initial, onDone }: { providers: ModelProviderInfo[]; initial: { provider: ProviderId; key?: ModelKey }; onDone(): void }) {
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
		const body: ModelKeyRequest = {
			provider,
			label: label.trim(),
			base_url: baseUrl.trim(),
			models: parseModels(modelsText),
			budget_usd: Number(budget) || 0,
			...(value.trim() ? { value: value.trim() } : {}),
		};
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
		<form className="bg-muted/30 grid gap-4 rounded-lg border p-4 sm:grid-cols-2" onSubmit={(e) => void onSubmit(e)} noValidate data-testid="model-key-form">
			<h2 className="font-semibold sm:col-span-2">{editing ? t("editTitle") : t("addTitle")}</h2>
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
				<Input id={`${id}-label`} value={label} placeholder={t("labelPlaceholder")} onChange={(e) => setLabel(e.target.value)} data-testid="model-key-label" />
			</div>
			<div className={field}>
				<label className={lbl} htmlFor={`${id}-value`}>
					{t("value")}
				</label>
				<Input
					id={`${id}-value`}
					type="password"
					autoComplete="off"
					value={value}
					placeholder={editing ? t("valueKeep") : (info?.key_prefix ?? "") + "..."}
					onChange={(e) => setValue(e.target.value)}
					aria-invalid={Boolean(valueError) || undefined}
					data-testid="model-key-value"
				/>
				{valueError && <p className="text-chart-error-ink text-xs">{valueError}</p>}
			</div>
			<div className={field}>
				<label className={lbl} htmlFor={`${id}-url`}>
					{t("baseUrl")}
				</label>
				<Input
					id={`${id}-url`}
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
					value={modelsText}
					placeholder={info?.models.map((m) => m.id).slice(0, 3).join(", ")}
					onChange={(e) => setModelsText(e.target.value)}
					data-testid="model-key-models"
				/>
				<p className="text-muted-foreground text-xs">{t("modelsHint")}</p>
			</div>
			<div className={field}>
				<label className={lbl} htmlFor={`${id}-budget`}>
					{t("budget")}
				</label>
				<Input id={`${id}-budget`} type="number" min={0} step="1" value={budget} onChange={(e) => setBudget(e.target.value)} data-testid="model-key-budget" />
				<p className="text-muted-foreground text-xs">{t("budgetHint")}</p>
			</div>
			{error && (
				<p role="alert" className="text-chart-error-ink text-xs sm:col-span-2" data-testid="model-key-error">
					{getBenchErrorMessage(error)}
				</p>
			)}
			<div className="flex gap-2 sm:col-span-2">
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
