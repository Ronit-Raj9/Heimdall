import { ForbiddenNote, RoleNote, isRoleRefusal } from "@/components/platform/accessNotes";
import { ConfirmButton } from "@/components/platform/badges";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { CopyId } from "@/components/ui/copyId";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useGetOrgQuery, useLeaveOrgMutation, useRenameOrgMutation, useSetPublicReadMutation } from "@/lib/store/apis/platformApi";
import type { OrgInfo, OrgSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Check, Eye, EyeOff, TriangleAlert } from "lucide-react";
import { useEffect, useId, useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { SettingsSection, When } from "./settingsBits";

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

/** A labelled row of the organization card. */
function Fact({ label, children }: { label: string; children: ReactNode }) {
	return (
		<div className="grid grid-cols-[8rem_minmax(0,1fr)] items-center gap-3 py-2.5 text-sm">
			<span className="text-muted-foreground">{label}</span>
			<span className="min-w-0">{children}</span>
		</div>
	);
}

/** The organization: name (admin renames), public read (owner) and the danger zone. */
export function OrgTab({ org }: { org: OrgSummary }) {
	const { t } = useTranslation("platform");
	const info = useGetOrgQuery(org.id);
	if (info.isLoading) return <BlockSkeleton rows={4} testId="org-loading" />;
	if (info.isError || !info.data) {
		return <LoadError title={t("settings.org.loadError")} error={info.error} onRetry={() => void info.refetch()} testId="org-error" />;
	}
	return <OrgSettings info={info.data} />;
}

function OrgSettings({ info }: { info: OrgInfo }) {
	const { t } = useTranslation("platform");
	const session = useSession();
	const formId = useId();
	const isAdmin = canUse(session, "admin");
	const isOwner = canUse(session, "owner");
	const [name, setName] = useState(info.name);
	useEffect(() => setName(info.name), [info.name]);
	const [rename, renameState] = useRenameOrgMutation();
	const [setPublic, publicState] = useSetPublicReadMutation();
	const [leave, leaveState] = useLeaveOrgMutation();
	const [saved, setSaved] = useState(false);

	const onRename = async (event: FormEvent) => {
		event.preventDefault();
		if (!name.trim() || name.trim() === info.name) return;
		try {
			await rename({ orgId: info.id, name: name.trim() }).unwrap();
			setSaved(true);
		} catch {
			// renameState.error renders the failure.
		}
	};
	const onLeave = async () => {
		if (!session.user) return;
		try {
			await leave({ orgId: info.id }).unwrap();
			window.location.assign("/");
		} catch {
			// leaveState.error renders the failure.
		}
	};

	return (
		<div className="flex flex-col gap-10" data-testid="settings-org">
			<SettingsSection
				title={t("settings.org.title")}
				description={t("settings.org.description", { org: info.name })}
				testId="settings-org-name"
			>
				<div className="divide-border flex flex-col divide-y rounded-lg border px-4">
					<Fact label={t("settings.org.slug")}>
						<span className="font-mono text-xs">{info.slug}</span>
					</Fact>
					<Fact label={t("settings.org.id")}>
						<CopyId value={info.id} className="text-xs" testId="org-id" />
					</Fact>
					<Fact label={t("settings.org.created")}>
						<When value={info.created_at} />
					</Fact>
				</div>
				{isAdmin ? (
					<form className="flex flex-wrap items-end gap-3" onSubmit={(event) => void onRename(event)} data-testid="org-rename-form">
						<div className="flex min-w-64 flex-1 flex-col gap-1.5">
							<label htmlFor={`${formId}-name`} className="text-[13px] font-medium">
								{t("settings.org.name")}
							</label>
							<Input
								id={`${formId}-name`}
								value={name}
								maxLength={120}
								onChange={(event) => {
									setName(event.target.value);
									setSaved(false);
								}}
								data-testid="org-name"
							/>
						</div>
						<Button
							type="submit"
							className="h-9"
							isLoading={renameState.isLoading}
							disabled={!name.trim() || name.trim() === info.name}
							dataTestId="org-rename"
						>
							{t("settings.org.rename")}
						</Button>
						{saved && (
							<span className="text-chart-success-ink flex h-9 items-center gap-1 text-xs" role="status">
								<Check className="size-3.5" aria-hidden="true" />
								{t("settings.org.saved")}
							</span>
						)}
					</form>
				) : (
					<RoleNote min="admin" />
				)}
				{renameState.error && (
					<WriteFailure error={renameState.error} title={t("settings.org.renameError")} testId="org-rename-error" min="admin" />
				)}
			</SettingsSection>

			<SettingsSection title={t("settings.org.publicTitle")} description={t("settings.org.publicBody")} testId="settings-org-public">
				<div
					className={cn(
						"flex flex-wrap items-center gap-3 rounded-lg border px-4 py-3",
						info.public_read && "border-chart-info/45 bg-chart-info/[0.06]",
					)}
					data-testid="org-public-state"
					data-public={info.public_read}
				>
					{info.public_read ? (
						<Eye className="text-chart-info-ink size-4" aria-hidden="true" />
					) : (
						<EyeOff className="text-muted-foreground size-4" aria-hidden="true" />
					)}
					<span className="text-foreground flex-1 text-sm font-medium">
						{info.public_read ? t("settings.org.publicOn") : t("settings.org.publicOff")}
					</span>
					{isOwner ? (
						<Button
							variant={info.public_read ? "outline" : "default"}
							size="sm"
							isLoading={publicState.isLoading}
							onClick={() => void setPublic({ orgId: info.id, publicRead: !info.public_read })}
							dataTestId="org-public-toggle"
						>
							{info.public_read ? t("settings.org.makePrivate") : t("settings.org.makePublic")}
						</Button>
					) : (
						<RoleNote min="owner" testId="org-public-owner-only" />
					)}
				</div>
				{publicState.error && (
					<WriteFailure error={publicState.error} title={t("settings.org.publicError")} testId="org-public-error" min="owner" />
				)}
			</SettingsSection>

			<SettingsSection title={t("settings.org.dangerTitle")} testId="settings-org-danger">
				<div className="border-chart-error/40 divide-chart-error/20 flex flex-col divide-y rounded-lg border">
					{session.user && (
						<div className="flex flex-wrap items-center gap-4 px-4 py-4">
							<div className="flex min-w-60 flex-1 flex-col gap-1">
								<span className="text-foreground text-sm font-medium">{t("settings.org.leaveTitle")}</span>
								<span className="text-muted-foreground text-xs">{t("settings.org.leaveBody", { org: info.name })}</span>
							</div>
							<ConfirmButton
								label={t("settings.org.leave")}
								question={t("settings.org.leaveConfirm", { org: info.name })}
								confirmLabel={t("settings.org.leave")}
								cancelLabel={t("settings.members.cancel")}
								onConfirm={() => void onLeave()}
								isLoading={leaveState.isLoading}
								variant="destructive"
								testId="org-leave"
							/>
						</div>
					)}
					<div className="flex items-start gap-3 px-4 py-4" data-testid="org-delete-note">
						<TriangleAlert className="text-chart-error-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
						<div className="flex flex-col gap-1">
							<span className="text-foreground text-sm font-medium">{t("settings.org.deleteTitle")}</span>
							<span className="text-muted-foreground text-xs">{t("settings.org.deleteBody")}</span>
						</div>
					</div>
				</div>
				{leaveState.error && (
					<WriteFailure error={leaveState.error} title={t("settings.org.leaveError")} testId="org-leave-error" min="admin" />
				)}
			</SettingsSection>
		</div>
	);
}