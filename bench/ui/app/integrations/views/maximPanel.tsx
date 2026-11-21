import { useConfirm } from "@/components/platform/confirmDialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { apiErrorMessage } from "@/lib/store/apis/integrationsApi";
import {
	useDeleteMaximMutation,
	useGetMaximQuery,
	useSaveMaximMutation,
	useTestMaximMutation,
} from "@/lib/store/apis/traceToolsApi";
import type { MaximConnection, MaximConnectionUpdate, MaximTestResult } from "@/lib/types/traceTools";
import { cn } from "@/lib/utils";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

export const MAXIM_LINK_PLACEHOLDER =
	"https://app.getmaxim.ai/workspace/{workspace_id}/log-repository/{repo_id}/logs?traceId={trace_id}";

export interface MaximDraft {
	apiKey: string;
	workspaceId: string;
	repoId: string;
	autoPush: boolean;
	linkTemplate: string;
}

export function maximDraft(conn: MaximConnection | undefined): MaximDraft {
	return {
		apiKey: "",
		workspaceId: conn?.workspace_id ?? "",
		repoId: conn?.repo_id ?? "",
		autoPush: conn?.auto_push ?? true,
		linkTemplate: conn?.link_template ?? "",
	};
}

/** PUT body: an empty key is left out so the stored key is kept. */
export function maximUpdateBody(d: MaximDraft): MaximConnectionUpdate {
	const body: MaximConnectionUpdate = { workspace_id: d.workspaceId.trim(), repo_id: d.repoId.trim(), auto_push: d.autoPush };
	if (d.apiKey.trim()) body.api_key = d.apiKey.trim();
	if (d.linkTemplate.trim()) body.link_template = d.linkTemplate.trim();
	return body;
}

/** Save needs a workspace, a repository and either a new key or a stored one. */
export function canSaveMaxim(d: MaximDraft, conn: MaximConnection | undefined): boolean {
	return Boolean(d.workspaceId.trim() && d.repoId.trim() && (d.apiKey.trim() || conn?.key_last4));
}

export interface MaximFormProps {
	conn?: MaximConnection;
	draft: MaximDraft;
	onDraft(next: MaximDraft): void;
	onSave(): void;
	onTest(): void;
	onDisconnect(): void;
	busy?: "save" | "test" | "delete" | null;
	message?: { ok: boolean; text: string } | null;
}

/** The Maxim connection form and its delivery counts. Pure: the panel wires the API. */
export function MaximForm({ conn, draft, onDraft, onSave, onTest, onDisconnect, busy = null, message = null }: MaximFormProps) {
	const { t } = useTranslation("traceTools");
	const set = (patch: Partial<MaximDraft>) => onDraft({ ...draft, ...patch });
	return (
		<form
			className="flex flex-col gap-4"
			data-testid="maxim-form"
			onSubmit={(e) => {
				e.preventDefault();
				onSave();
			}}
		>
			<div className="flex flex-wrap items-center gap-3 text-sm">
				<span
					className={cn(
						"inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs",
						conn?.connected ? "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400" : "bg-muted text-muted-foreground",
					)}
					data-testid="maxim-status"
				>
					<span className={cn("size-1.5 rounded-full", conn?.connected ? "bg-emerald-500" : "bg-muted-foreground/50")} aria-hidden="true" />
					{conn?.connected ? t("maxim.connected") : t("maxim.notConnected")}
				</span>
				{conn?.connected && (
					<span className="text-muted-foreground text-xs tabular-nums" data-testid="maxim-counts">
						{t("maxim.pushed")} {conn.pushed} · {t("maxim.failed")} {conn.failed}
					</span>
				)}
			</div>
			{conn?.last_error && (
				<p className="text-chart-error-ink text-xs break-words" data-testid="maxim-last-error">
					{t("maxim.lastError", { error: conn.last_error })}
				</p>
			)}
			<div className="grid gap-3 sm:grid-cols-3">
				<label className="flex flex-col gap-1 text-sm">
					<span className="font-medium">{t("maxim.apiKey")}</span>
					<Input
						type="password"
						autoComplete="off"
						value={draft.apiKey}
						placeholder={t("maxim.apiKeyPlaceholder")}
						onChange={(e) => set({ apiKey: e.target.value })}
						data-testid="maxim-api-key"
					/>
					{conn?.key_last4 && (
						<span className="text-muted-foreground text-xs" data-testid="maxim-stored-key">
							{t("maxim.storedKey", { last4: conn.key_last4 })}
						</span>
					)}
				</label>
				<label className="flex flex-col gap-1 text-sm">
					<span className="font-medium">{t("maxim.workspace")}</span>
					<Input value={draft.workspaceId} onChange={(e) => set({ workspaceId: e.target.value })} data-testid="maxim-workspace" />
				</label>
				<label className="flex flex-col gap-1 text-sm">
					<span className="font-medium">{t("maxim.repo")}</span>
					<Input value={draft.repoId} onChange={(e) => set({ repoId: e.target.value })} data-testid="maxim-repo" />
				</label>
			</div>
			<label className="flex w-fit cursor-pointer items-center gap-2 text-sm">
				<button
					type="button"
					role="switch"
					aria-checked={draft.autoPush}
					onClick={() => set({ autoPush: !draft.autoPush })}
					className={cn(
						"focus-visible:ring-ring/50 relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors focus-visible:ring-2 focus-visible:outline-none",
						draft.autoPush ? "bg-primary" : "bg-muted-foreground/30",
					)}
					data-testid="maxim-auto-push"
				>
					<span
						className={cn("bg-background inline-block size-4 rounded-full shadow-sm transition-transform", draft.autoPush ? "translate-x-4.5" : "translate-x-0.5")}
					/>
				</button>
				{t("maxim.autoPush")}
			</label>
			<details className="text-sm">
				<summary className="text-muted-foreground hover:text-foreground cursor-pointer text-xs">{t("maxim.advanced")}</summary>
				<label className="mt-2 flex flex-col gap-1">
					<span className="font-medium">{t("maxim.linkTemplate")}</span>
					<Input
						value={draft.linkTemplate}
						placeholder={MAXIM_LINK_PLACEHOLDER}
						onChange={(e) => set({ linkTemplate: e.target.value })}
						className="font-mono text-xs"
						data-testid="maxim-link-template"
					/>
					<span className="text-muted-foreground text-xs">{t("maxim.linkHelp")}</span>
				</label>
			</details>
			<div className="flex flex-wrap items-center gap-2">
				<Button type="submit" disabled={!canSaveMaxim(draft, conn) || busy !== null} data-testid="maxim-save">
					{busy === "save" ? t("maxim.saving") : t("maxim.save")}
				</Button>
				<Button type="button" variant="outline" disabled={!conn?.connected || busy !== null} onClick={onTest} data-testid="maxim-test">
					{busy === "test" ? t("maxim.testing") : t("maxim.test")}
				</Button>
				{conn?.connected && (
					<Button type="button" variant="ghost" disabled={busy !== null} onClick={onDisconnect} data-testid="maxim-disconnect">
						{t("maxim.disconnect")}
					</Button>
				)}
				{message && (
					<span
						className={cn("text-sm", message.ok ? "text-chart-success-ink" : "text-chart-error-ink")}
						role={message.ok ? "status" : "alert"}
						data-testid="maxim-message"
					>
						{message.text}
					</span>
				)}
			</div>
		</form>
	);
}

function testMessage(r: MaximTestResult, okText: string): string {
	if (!r.ok) return r.message;
	return r.repo_name ? `${okText}: ${r.repo_name}` : r.message || okText;
}

/** Integrations > Maxim: connect a Maxim log repository so finished runs are sent there. */
export function MaximPanel() {
	const { t } = useTranslation("traceTools");
	const confirm = useConfirm();
	const conn = useGetMaximQuery();
	const [save] = useSaveMaximMutation();
	const [remove] = useDeleteMaximMutation();
	const [test] = useTestMaximMutation();
	const [draft, setDraft] = useState<MaximDraft>(maximDraft(undefined));
	const [busy, setBusy] = useState<MaximFormProps["busy"]>(null);
	const [message, setMessage] = useState<MaximFormProps["message"]>(null);
	const loaded = conn.data;
	useEffect(() => {
		if (loaded) setDraft(maximDraft(loaded));
	}, [loaded]);
	const fail = (err: unknown) => setMessage({ ok: false, text: apiErrorMessage(err, t("maxim.genericError")) });

	return (
		<section className="border-border bg-card flex flex-col gap-4 rounded-xl border p-5" data-testid="maxim-panel">
			<div className="flex flex-col gap-0.5">
				<h2 className="text-base font-semibold">{t("maxim.tab")}</h2>
				<p className="text-muted-foreground text-sm">{t("maxim.desc")}</p>
			</div>
			{conn.error ? (
				<p className="text-chart-error-ink text-sm" role="alert" data-testid="maxim-load-error">
					{apiErrorMessage(conn.error, t("maxim.loadError"))}
				</p>
			) : conn.isLoading ? null : (
				<MaximForm
					conn={conn.data}
					draft={draft}
					onDraft={setDraft}
					busy={busy}
					message={message}
					onSave={async () => {
						setMessage(null);
						setBusy("save");
						const res = await save(maximUpdateBody(draft));
						setBusy(null);
						if ("error" in res) return fail(res.error);
						setMessage({ ok: true, text: t("maxim.saved") });
					}}
					onTest={async () => {
						setMessage(null);
						setBusy("test");
						const res = await test();
						setBusy(null);
						if ("error" in res) return fail(res.error);
						setMessage({ ok: res.data.ok, text: testMessage(res.data, t("maxim.testOk")) });
					}}
					onDisconnect={async () => {
						if (!(await confirm(t("maxim.disconnectConfirm")))) return;
						setMessage(null);
						setBusy("delete");
						const res = await remove();
						setBusy(null);
						if ("error" in res) fail(res.error);
					}}
				/>
			)}
		</section>
	);
}
