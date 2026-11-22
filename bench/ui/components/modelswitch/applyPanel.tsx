import "@/lib/types/modelSwitchI18n";
import { Button } from "@/components/ui/button";
import { type ModelSwitchApplyResult, useApplyModelSwitchMutation } from "@/lib/store/apis/modelSwitchApi";
import { useListSecretsQuery } from "@/lib/store/apis/secretsApi";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/**
 * "Apply to my Bifrost" (lane Z21): writes the routing rules to the org's own gateway through the
 * apply API. Preview (dry run) first; rules start off unless the admin turns on the proven ones.
 * The server allows this for org admins only and answers 403 otherwise, shown here as the error.
 * Rules are scoped to a team, customer or virtual key by default; global needs the explicit check.
 * The credential is picked from the org's secrets by name (values never reach the page).
 */
export const APPLY_SCOPES = ["virtual_key", "team", "customer", "global"] as const;
export type ApplyScope = (typeof APPLY_SCOPES)[number];

/** Whether the form may send: a gateway, and a scope id, or global with the explicit check. */
export function applyReady(url: string, scope: ApplyScope, scopeId: string, allowGlobal: boolean): boolean {
	if (url.trim() === "") return false;
	return scope === "global" ? allowGlobal : scopeId.trim() !== "";
}

export function ApplyPanel({ evaluationId, ruleCount }: { evaluationId: string; ruleCount: number }) {
	const { t } = useTranslation("modelSwitch");
	const [url, setUrl] = useState("");
	const [secret, setSecret] = useState("");
	const [enable, setEnable] = useState(false);
	const [allowGlobal, setAllowGlobal] = useState(false);
	const [scope, setScope] = useState<ApplyScope>("virtual_key");
	const [scopeId, setScopeId] = useState("");
	const secrets = useListSecretsQuery();
	const secretNames = (secrets.data?.secrets ?? []).map((s) => s.name).sort();
	const [apply, state] = useApplyModelSwitchMutation();
	const [result, setResult] = useState<ModelSwitchApplyResult | null>(null);
	const run = async (dry: boolean) => {
		if (!dry && !window.confirm(t("export.apply.confirm", { count: ruleCount, url }))) return;
		const res = await apply({
			id: evaluationId,
			body: {
				gateway_url: url.trim(),
				...(secret.trim() ? { credential_secret: secret.trim() } : {}),
				scope,
				...(scope !== "global" ? { scope_id: scopeId.trim() } : {}),
				enable,
				allow_global: scope === "global" && allowGlobal,
				dry_run: dry,
			},
		});
		setResult("data" in res && res.data ? res.data : null);
	};
	const err = state.error as { data?: { error?: { message?: string } } } | undefined;
	const ready = applyReady(url, scope, scopeId, allowGlobal);
	return (
		<div className="flex flex-col gap-2 rounded-lg border p-3 text-sm" data-testid="model-switch-apply">
			<span className="font-medium">{t("export.apply.title")}</span>
			<span className="text-muted-foreground">{t("export.apply.description")}</span>
			<label className="flex flex-col gap-1">
				{t("export.apply.url")}
				<input className="border-input bg-background h-9 rounded-lg border px-2" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://bifrost.example.com" data-testid="model-switch-apply-url" />
			</label>
			<label className="flex flex-col gap-1">
				{t("export.apply.secret")}
				<select className="border-input bg-background h-9 rounded-lg border px-2" value={secret} onChange={(e) => setSecret(e.target.value)} data-testid="model-switch-apply-secret">
					<option value="">{t("export.apply.secretNone")}</option>
					{secretNames.map((n) => (
						<option key={n} value={n}>
							{n}
						</option>
					))}
				</select>
				<span className="text-muted-foreground text-xs">{secrets.error ? t("export.apply.secretLoadError") : t("export.apply.secretHint")}</span>
			</label>
			<label className="flex flex-col gap-1">
				{t("export.apply.scope")}
				<select
					className="border-input bg-background h-9 rounded-lg border px-2"
					value={scope}
					onChange={(e) => setScope(e.target.value as ApplyScope)}
					data-testid="model-switch-apply-scope"
				>
					{APPLY_SCOPES.map((s) => (
						<option key={s} value={s}>
							{t(`export.apply.scopes.${s}`)}
						</option>
					))}
				</select>
			</label>
			{scope !== "global" && (
				<label className="flex flex-col gap-1">
					{t(`export.apply.scopeId.${scope}`)}
					<input className="border-input bg-background h-9 rounded-lg border px-2" value={scopeId} onChange={(e) => setScopeId(e.target.value)} data-testid="model-switch-apply-scope-id" />
				</label>
			)}
			<label className="flex items-center gap-2">
				<input type="checkbox" checked={enable} onChange={(e) => setEnable(e.target.checked)} data-testid="model-switch-apply-enable" />
				{t("export.apply.enable")}
			</label>
			{scope === "global" && (
				<label className="flex items-center gap-2">
					<input type="checkbox" checked={allowGlobal} onChange={(e) => setAllowGlobal(e.target.checked)} data-testid="model-switch-apply-global" />
					{t("export.apply.allowGlobal")}
				</label>
			)}
			<div className="flex gap-2">
				<Button size="sm" variant="outline" disabled={!ready || state.isLoading} onClick={() => run(true)} dataTestId="model-switch-apply-preview">
					{t("export.apply.dryRun")}
				</Button>
				<Button size="sm" disabled={!ready || state.isLoading} onClick={() => run(false)} dataTestId="model-switch-apply-run">
					{t("export.apply.run")}
				</Button>
			</div>
			{err && (
				<p className="text-destructive" role="alert">
					{t("export.apply.error", { message: err.data?.error?.message ?? "" })}
				</p>
			)}
			{result && <ApplySteps result={result} />}
		</div>
	);
}

export function ApplySteps({ result }: { result: ModelSwitchApplyResult }) {
	const { t } = useTranslation("modelSwitch");
	return (
		<ul className="flex flex-col gap-1" data-testid="model-switch-apply-steps">
			{result.steps.map((s) => (
				<li key={s.rule} data-status={s.status}>
					{s.status === "failed"
						? t("export.apply.failed", { rule: s.rule, error: s.error ?? "" })
						: t(s.status === "planned" ? "export.apply.planned" : "export.apply.done", { action: s.action, rule: s.rule, priority: s.priority })}
				</li>
			))}
			{result.stale.length > 0 && <li>{t("export.apply.stale", { rules: result.stale.join(", ") })}</li>}
			{result.notes.map((n) => (
				<li key={n} className="text-muted-foreground">
					{n}
				</li>
			))}
		</ul>
	);
}
