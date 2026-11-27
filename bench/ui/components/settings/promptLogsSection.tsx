import { RoleNote } from "@/components/platform/accessNotes";
import { ConfirmButton } from "@/components/platform/badges";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	retentionChoices,
	useDeletePromptLogsMutation,
	useGetPromptLoggingQuery,
	useListPromptLogsQuery,
	useSetPromptLoggingMutation,
	type PromptLogging,
} from "@/lib/store/apis/promptLogsApi";
import { cn } from "@/lib/utils";
import { Check, FileText, FileX } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { SettingsSection } from "./settingsBits";
import { useLeaveGuard } from "@/lib/hooks/useLeaveGuard";
import i18n from "@/lib/i18n";
import "@/components/settings/uf8aI18n";

/** Prompt logging of the organization: keep text or not, retention, delete all (admin). */
export function PromptLogsSection() {
	const { t } = useTranslation("platform");
	const policy = useGetPromptLoggingQuery();
	return (
		<SettingsSection title={t("settings.promptLogs.title")} description={t("settings.promptLogs.description")} testId="settings-prompt-logs">
			{policy.isLoading ? (
				<BlockSkeleton rows={2} testId="prompt-logs-loading" />
			) : policy.isError || !policy.data ? (
				<LoadError
					title={t("settings.promptLogs.loadError")}
					error={policy.error}
					onRetry={() => void policy.refetch()}
					testId="prompt-logs-error"
				/>
			) : (
				<PromptLogsForm policy={policy.data} />
			)}
		</SettingsSection>
	);
}

function PromptLogsForm({ policy }: { policy: PromptLogging }) {
	const { t } = useTranslation("platform");
	const session = useSession();
	const isAdmin = canUse(session, "admin");
	const [keep, setKeep] = useState(policy.keep_content);
	const [days, setDays] = useState(policy.retention_days);
	const [saved, setSaved] = useState(false);
	useEffect(() => {
		setKeep(policy.keep_content);
		setDays(policy.retention_days);
	}, [policy.keep_content, policy.retention_days]);
	const [save, saveState] = useSetPromptLoggingMutation();
	const rows = useListPromptLogsQuery({ limit: 1 }, { skip: !policy.gateway_logs });
	const [purge, purgeState] = useDeletePromptLogsMutation();
	const dirty = keep !== policy.keep_content || days !== policy.retention_days;
	useLeaveGuard(isAdmin && dirty && !saved, { body: i18n.t("unsaved.body", { ns: "uf8a" }) });

	const onSave = async () => {
		try {
			await save({ keep_content: keep, retention_days: days }).unwrap();
			setSaved(true);
		} catch {
			// saveState.error renders the failure.
		}
	};

	return (
		<div className="flex flex-col gap-4" data-testid="prompt-logs-form">
			<PromptLogsState
				keep={keep}
				days={days}
				choices={retentionChoices(policy.max_retention_days, policy.retention_days)}
				canWrite={isAdmin}
				onKeep={(next) => {
					setKeep(next);
					setSaved(false);
				}}
				onDays={(next) => {
					setDays(next);
					setSaved(false);
				}}
			/>
			{isAdmin ? (
				<div className="flex items-center gap-3">
					<Button size="sm" isLoading={saveState.isLoading} disabled={!dirty} onClick={() => void onSave()} dataTestId="prompt-logs-save">
						{t("settings.promptLogs.save")}
					</Button>
					{saved && !dirty && (
						<span className="text-chart-success-ink flex items-center gap-1 text-xs" role="status">
							<Check className="size-3.5" aria-hidden="true" />
							{t("settings.promptLogs.saved")}
						</span>
					)}
				</div>
			) : (
				<RoleNote min="admin" testId="prompt-logs-admin-only" />
			)}
			{saveState.error && (
				<p role="alert" className="text-chart-error-ink text-xs" data-testid="prompt-logs-save-error">
					{t("settings.promptLogs.saveError")}: {getBenchErrorMessage(saveState.error)}
				</p>
			)}
			{policy.gateway_logs ? (
				<div className="flex flex-wrap items-center gap-4 rounded-lg border px-4 py-4" data-testid="prompt-logs-delete">
					<div className="flex min-w-60 flex-1 flex-col gap-1">
						<span className="text-foreground text-sm font-medium">{t("settings.promptLogs.deleteTitle")}</span>
						<span className="text-muted-foreground text-xs">{t("settings.promptLogs.deleteBody")}</span>
						{rows.data && (
							<span className="text-muted-foreground text-xs tabular-nums" data-testid="prompt-logs-count">
								{t("settings.promptLogs.rows", { count: rows.data.total })}
							</span>
						)}
						{purgeState.data && (
							<span className="text-chart-success-ink text-xs" role="status" data-testid="prompt-logs-deleted">
								{t("settings.promptLogs.deleted", { count: purgeState.data.deleted })}
							</span>
						)}
					</div>
					{isAdmin && (
						<ConfirmButton
							label={t("settings.promptLogs.delete")}
							question={t("settings.promptLogs.deleteConfirm")}
							confirmLabel={t("settings.promptLogs.delete")}
							cancelLabel={t("settings.promptLogs.cancel")}
							onConfirm={() => void purge().unwrap().catch(() => undefined)}
							isLoading={purgeState.isLoading}
							variant="destructive"
							testId="prompt-logs-delete-button"
						/>
					)}
				</div>
			) : (
				<p className="text-muted-foreground text-xs" data-testid="prompt-logs-no-gateway">
					{t("settings.promptLogs.noGateway")}
				</p>
			)}
			{purgeState.error && (
				<p role="alert" className="text-chart-error-ink text-xs" data-testid="prompt-logs-delete-error">
					{t("settings.promptLogs.deleteError")}: {getBenchErrorMessage(purgeState.error)}
				</p>
			)}
		</div>
	);
}

export interface PromptLogsStateProps {
	keep: boolean;
	days: number;
	choices: number[];
	canWrite: boolean;
	onKeep: (keep: boolean) => void;
	onDays: (days: number) => void;
}

/** The keep-text switch and the retention picker; read only for members and viewers. */
export function PromptLogsState({ keep, days, choices, canWrite, onKeep, onDays }: PromptLogsStateProps) {
	const { t } = useTranslation("platform");
	return (
		<div className="divide-border flex flex-col divide-y rounded-lg border">
			<div
				className={cn("flex flex-wrap items-center gap-3 px-4 py-3", !keep && "bg-chart-info/[0.06]")}
				data-testid="prompt-logs-keep"
				data-keep={keep}
			>
				{keep ? (
					<FileText className="text-muted-foreground size-4" aria-hidden="true" />
				) : (
					<FileX className="text-chart-info-ink size-4" aria-hidden="true" />
				)}
				<div className="flex min-w-60 flex-1 flex-col gap-0.5">
					<span className="text-foreground text-sm font-medium">{t("settings.promptLogs.keepTitle")}</span>
					<span className="text-muted-foreground text-xs">{keep ? t("settings.promptLogs.keepOn") : t("settings.promptLogs.keepOff")}</span>
				</div>
				{canWrite && (
					<Button variant="outline" size="sm" onClick={() => onKeep(!keep)} dataTestId="prompt-logs-keep-toggle">
						{keep ? t("settings.promptLogs.turnOff") : t("settings.promptLogs.turnOn")}
					</Button>
				)}
			</div>
			<div className="flex flex-wrap items-center gap-3 px-4 py-3">
				<span className="text-foreground flex-1 text-sm font-medium">{t("settings.promptLogs.retention")}</span>
				{canWrite ? (
					<Select value={String(days)} onValueChange={(v) => onDays(Number(v))}>
						<SelectTrigger size="sm" className="min-w-28" aria-label={t("settings.promptLogs.retention")} data-testid="prompt-logs-retention">
							<SelectValue />
						</SelectTrigger>
						<SelectContent position="popper">
							{choices.map((d) => (
								<SelectItem key={d} value={String(d)} data-testid={`prompt-logs-retention-${d}`}>
									{t("settings.promptLogs.days", { count: d })}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				) : (
					<span className="text-sm tabular-nums" data-testid="prompt-logs-retention-value">
						{t("settings.promptLogs.days", { count: days })}
					</span>
				)}
			</div>
		</div>
	);
}
