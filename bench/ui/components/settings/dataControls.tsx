import { ForbiddenNote, RoleNote, isRoleRefusal } from "@/components/platform/accessNotes";
import { useConfirm } from "@/components/platform/confirmDialog";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useDeleteOrgMutation, useGetRetentionQuery, useSetRetentionMutation } from "@/lib/store/apis/dataOpsApi";
import { useLazyGetRetentionPreviewQuery, useSwitchOrgMutation } from "@/lib/store/apis/platformApi";
import type { RetentionPreview, RetentionWithPreview } from "@/lib/types/serverFields";
import type { RetentionPolicy } from "@/lib/types/dataops";
import type { OrgInfo } from "@/lib/types/bench";
import { Check, TriangleAlert } from "lucide-react";
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { SettingsSection, When } from "./settingsBits";
import "@/components/calibration/hw04I18n";
import "./uf8aI18n";
import { useLeaveGuard } from "@/lib/hooks/useLeaveGuard";
import i18n from "@/lib/i18n";
import { formatCount } from "@/lib/numbers";
import { Dialog } from "@/components/ui/dialog";

/**
 * The retention dry run (G1, M8-55): GET /data/retention?preview_days=N answers the policy plus how
 * many comparisons and runs a shorter retention would delete. Servers without the field leave
 * `preview` out; the plain confirm stays then. The preview of the answer when it is well formed and
 * for the asked days, else null.
 */
export function previewOf(answer: RetentionWithPreview | undefined, days: number): RetentionPreview | null {
	const p = answer?.preview;
	if (!p || p.days !== days || typeof p.runs !== "number" || typeof p.experiments !== "number") return null;
	return p;
}

/**
 * True when a new retention deletes runs that the old one keeps: any limit after "forever" (0),
 * or fewer days than before. Saving such a change asks first.
 */
export function retentionShrinks(oldDays: number, newDays: number): boolean {
	if (newDays <= 0) return false;
	return oldDays <= 0 || newDays < oldDays;
}

/** The preset retention choices in days; 0 keeps everything forever. */
export const RETENTION_PRESETS = [0, 30, 90, 180, 365] as const;

/** The select value for a stored policy: a preset's day count, or custom. */
export function retentionChoice(days: number): string {
	return (RETENTION_PRESETS as readonly number[]).includes(days) ? String(days) : "custom";
}

/** The run_days to save from the form, or null when the custom number is missing or out of range. */
export function retentionDays(choice: string, custom: string, policy: Pick<RetentionPolicy, "min_days" | "max_days">): number | null {
	if (choice !== "custom") return Number(choice);
	const trimmed = custom.trim();
	if (!/^\d+$/.test(trimmed)) return null;
	const days = Number(trimmed);
	return days >= policy.min_days && days <= policy.max_days ? days : null;
}

/** A failed write: the role refusal when it is one, the server's reason otherwise. */
function WriteFailure({ error, title, testId, min }: { error: unknown; title: string; testId: string; min: "admin" | "owner" }) {
	const session = useSession();
	if (isRoleRefusal(session, error)) return <ForbiddenNote min={min} error={error} testId={testId} />;
	return (
		<p role="alert" className="text-chart-error-ink text-xs" data-testid={testId}>
			{title}: {getBenchErrorMessage(error)}
		</p>
	);
}

/** Settings > Organization: how long comparisons and their runs are kept. Admins change it. */
export function RetentionSection() {
	const { t } = useTranslation("platform");
	const policy = useGetRetentionQuery();
	return (
		<SettingsSection title={t("settings.org.retentionTitle")} description={t("settings.org.retentionBody")} testId="settings-org-retention">
			{policy.isLoading ? (
				<BlockSkeleton rows={2} testId="retention-loading" />
			) : policy.isError || !policy.data ? (
				<LoadError
					title={t("settings.org.retentionLoadError")}
					error={policy.error}
					onRetry={() => void policy.refetch()}
					testId="retention-error"
				/>
			) : (
				<RetentionForm policy={policy.data} />
			)}
		</SettingsSection>
	);
}

/** The retention form for a loaded policy. Exported for tests. */
export function RetentionForm({ policy }: { policy: RetentionPolicy }) {
	const { t } = useTranslation("platform");
	const { t: tHw04 } = useTranslation("hw04");
	const { t: tl } = useTranslation("uf8a");
	const session = useSession();
	const confirm = useConfirm();
	const formId = useId();
	const canEdit = canUse(session, "admin");
	const [choice, setChoice] = useState(retentionChoice(policy.run_days));
	const [custom, setCustom] = useState(choice === "custom" ? String(policy.run_days) : "");
	const [saved, setSaved] = useState(false);
	const [save, state] = useSetRetentionMutation();
	const [fetchPreview] = useLazyGetRetentionPreviewQuery();
	useEffect(() => {
		const next = retentionChoice(policy.run_days);
		setChoice(next);
		setCustom(next === "custom" ? String(policy.run_days) : "");
	}, [policy.run_days]);
	const days = retentionDays(choice, custom, policy);
	const changed = days !== null && days !== policy.run_days;
	useLeaveGuard(canEdit && changed && !saved, { body: i18n.t("unsaved.body", { ns: "uf8a" }) });

	const onSave = async (event: FormEvent) => {
		event.preventDefault();
		if (days === null || !changed) return;
		// A shorter retention deletes old comparisons at the next sweep: say so and ask first.
		// The dry run count is shown when the server sends it; without it the plain question stays.
		if (retentionShrinks(policy.run_days, days)) {
			const preview = await fetchPreview(days)
				.unwrap()
				.then((answer) => previewOf(answer, days))
				.catch(() => null);
			const question = preview
				? tl("retention.confirmCount", { days, runs: formatCount(preview.runs), experiments: formatCount(preview.experiments) })
				: tl("retention.confirm", { count: days });
			if (!(await confirm(question, { title: tl("retention.confirmTitle"), confirmLabel: tl("retention.confirmLabel"), destructive: true })))
				return;
		}
		try {
			await save(days).unwrap();
			setSaved(true);
		} catch {
			// state.error renders the failure.
		}
	};

	return (
		<div className="flex flex-col gap-3">
			<p className="text-foreground text-sm" data-testid="retention-current">
				{policy.run_days === 0 ? t("settings.org.retentionCurrentForever") : t("settings.org.retentionCurrent", { count: policy.run_days })}
			</p>
			{(policy.updated_by_name || policy.updated_by) && (
				<p className="text-muted-foreground text-xs" title={policy.updated_by || undefined} data-testid="retention-changed-by">
					{tHw04("retention.changedBy", { who: policy.updated_by_name || policy.updated_by })}
					{policy.updated_at && (
						<>
							{" "}
							<When value={policy.updated_at} />
						</>
					)}
				</p>
			)}
			{policy.last_sweep_at && (
				<p className="text-muted-foreground text-xs" data-testid="retention-last-sweep">
					{t("settings.org.retentionLastSweepAt")} <When value={policy.last_sweep_at} />.{" "}
					{t("settings.org.retentionLastSweep", { count: policy.last_deleted ?? 0 })}
				</p>
			)}
			{canEdit ? (
				<form className="flex flex-wrap items-end gap-3" onSubmit={(event) => void onSave(event)} data-testid="retention-form">
					<div className="flex flex-col gap-1.5">
						<span className="text-[13px] font-medium">{t("settings.org.retentionLabel")}</span>
						<Select
							value={choice}
							onValueChange={(value) => {
								setChoice(value);
								setSaved(false);
							}}
						>
							<SelectTrigger className="min-w-40" aria-label={t("settings.org.retentionLabel")} data-testid="retention-choice">
								<SelectValue />
							</SelectTrigger>
							<SelectContent position="popper">
								{RETENTION_PRESETS.map((preset) => (
									<SelectItem key={preset} value={String(preset)}>
										{preset === 0 ? t("settings.org.retentionForever") : t("settings.org.retentionDays", { count: preset })}
									</SelectItem>
								))}
								<SelectItem value="custom">{t("settings.org.retentionCustom")}</SelectItem>
							</SelectContent>
						</Select>
					</div>
					{choice === "custom" && (
						<div className="flex w-32 flex-col gap-1.5">
							<label htmlFor={`${formId}-days`} className="text-[13px] font-medium">
								{t("settings.org.retentionCustomLabel")}
							</label>
							<Input
								id={`${formId}-days`}
								type="number"
								inputMode="numeric"
								min={policy.min_days}
								max={policy.max_days}
								value={custom}
								onChange={(event) => {
									setCustom(event.target.value);
									setSaved(false);
								}}
								data-testid="retention-days"
							/>
						</div>
					)}
					<Button type="submit" className="h-9" isLoading={state.isLoading} disabled={!changed} dataTestId="retention-save">
						{t("settings.org.retentionSave")}
					</Button>
					{saved && (
						<span className="text-chart-success-ink flex h-9 items-center gap-1 text-xs" role="status">
							<Check className="size-3.5" aria-hidden="true" />
							{t("settings.org.retentionSaved")}
						</span>
					)}
				</form>
			) : (
				<RoleNote min="admin" testId="retention-admin-only" />
			)}
			{choice === "custom" && canEdit && (
				<p className="text-muted-foreground text-xs">{t("settings.org.retentionRange", { min: policy.min_days, max: policy.max_days })}</p>
			)}
			{state.error && (
				<WriteFailure error={state.error} title={t("settings.org.retentionError")} testId="retention-save-error" min="admin" />
			)}
		</div>
	);
}

/** Danger zone row: delete the organization (hosted, owner, signed-in user). */
export function DeleteOrgRow({ info }: { info: OrgInfo }) {
	const { t } = useTranslation("platform");
	const session = useSession();
	const [open, setOpen] = useState(false);
	const isOwner = session.authMode === "hosted" && Boolean(session.user) && canUse(session, "owner");
	return (
		<div className="flex flex-wrap items-center gap-4 px-4 py-4" data-testid="org-delete-note">
			<TriangleAlert className="text-chart-error-ink size-4 shrink-0" aria-hidden="true" />
			<div className="flex min-w-60 flex-1 flex-col gap-1">
				<span className="text-foreground text-sm font-medium">{t("settings.org.deleteTitle")}</span>
				<span className="text-muted-foreground text-xs">{t("settings.org.deleteBody", { org: info.name })}</span>
			</div>
			{isOwner ? (
				<Button variant="destructive" size="sm" className="h-7 px-2.5 text-xs" onClick={() => setOpen(true)} dataTestId="org-delete">
					{t("settings.org.deleteOrg")}
				</Button>
			) : (
				<span className="text-muted-foreground text-xs" data-testid="org-delete-owner-only">
					{t("settings.org.deleteOwnerOnly")}
				</span>
			)}
			{open && <DeleteOrgDialog info={info} onClose={() => setOpen(false)} />}
		</div>
	);
}

/** The delete dialog: the owner types the org's short name exactly before Delete unlocks. Exported for tests. */
export function DeleteOrgDialog({ info, onClose }: { info: OrgInfo; onClose(): void }) {
	const { t } = useTranslation("platform");
	const session = useSession();
	const { t: tl } = useTranslation("uf8a");
	const formId = useId();
	const inputId = useId();
	const inputRef = useRef<HTMLInputElement>(null);
	const [typed, setTyped] = useState("");
	const [remove, state] = useDeleteOrgMutation();
	const [switchOrg] = useSwitchOrgMutation();
	const matches = typed === info.slug;
	const onDelete = async (event: FormEvent) => {
		event.preventDefault();
		if (!matches) return;
		try {
			await remove({ orgId: info.id, confirm: typed }).unwrap();
			// Same as leaving: the session still names the deleted org, so make another membership
			// current first. With none left the user lands on the empty app and its org picker.
			const next = session.orgs.find((candidate) => candidate.id !== info.id);
			if (next)
				await switchOrg({ org: next.id })
					.unwrap()
					.catch(() => undefined);
			window.location.assign("/");
		} catch {
			// state.error renders the failure.
		}
	};

	const close = (open: boolean) => {
		if (!open && !state.isLoading) onClose();
	};
	return (
		<Dialog
			open
			onOpenChange={close}
			role="alertdialog"
			size="sm"
			title={t("settings.org.deleteDialogTitle", { org: info.name })}
			description={t("settings.org.deleteDialogBody")}
			closeLabel={tl("dialog.close")}
			initialFocus={inputRef}
			testId="org-delete-dialog"
		>
			<form id={formId} className="space-y-4" onSubmit={(event) => void onDelete(event)} data-testid="org-delete-form">
				<div className="flex flex-col gap-1.5">
					<label htmlFor={inputId} className="text-[13px] font-medium">
						{t("settings.org.deleteTypeSlug", { slug: info.slug })}
					</label>
					<Input
						ref={inputRef}
						id={inputId}
						className="font-mono"
						autoComplete="off"
						spellCheck={false}
						value={typed}
						onChange={(event) => setTyped(event.target.value)}
						data-testid="org-delete-confirm-input"
					/>
				</div>
				{state.error && <WriteFailure error={state.error} title={t("settings.org.deleteError")} testId="org-delete-error" min="owner" />}
				<div className="flex justify-end gap-2">
					<Button type="button" variant="outline" onClick={onClose} disabled={state.isLoading} dataTestId="org-delete-cancel">
						{t("settings.org.deleteCancel")}
					</Button>
					<Button type="submit" variant="destructive" disabled={!matches} isLoading={state.isLoading} dataTestId="org-delete-submit">
						{t("settings.org.deleteConfirm")}
					</Button>
				</div>
			</form>
		</Dialog>
	);
}
