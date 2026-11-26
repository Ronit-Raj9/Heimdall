import { RoleGate, isRoleRefusal, ForbiddenNote } from "@/components/platform/accessNotes";
import { ConfirmButton, RoleBadge } from "@/components/platform/badges";
import { OneTimeSecret } from "@/components/platform/oneTimeSecret";
import { looksLikeEmail } from "@/components/platform/platform.utils";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatRelative } from "@/components/platform/platform.utils";
import { getLocale } from "@/lib/i18n";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useCreateResetLinkMutation } from "@/lib/store/apis/accountSafetyApi";
import type { ResetLink } from "@/lib/types/accountSafety";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	useCreateInvitationMutation,
	useListInvitationsQuery,
	useListMembersQuery,
	useRemoveMemberMutation,
	useRevokeInvitationMutation,
	useUpdateMemberMutation,
} from "@/lib/store/apis/platformApi";
import type { Invitation, Member, OrgSummary, Role } from "@/lib/types/bench";
import { UserPlus } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { RoleSelect, SettingsSection, TableFrame, When, grantableRoles, mayManage } from "./settingsBits";
import "./hw03I18n";
import "@/components/shell/w4b07I18n";

/** The link an invitation token opens, on this site. */
export function invitationLink(origin: string, token: string): string {
	return `${origin}/invite/${encodeURIComponent(token)}`;
}

function pageOrigin(): string {
	return typeof window === "undefined" ? "" : window.location.origin;
}

/** A failed write shown inline: the role refusal when it is one, the server's reason otherwise. */
function WriteError({ error, title, testId }: { error: unknown; title: string; testId: string }) {
	const session = useSession();
	if (isRoleRefusal(session, error)) return <ForbiddenNote min="admin" error={error} testId={testId} />;
	return (
		<p role="alert" className="text-chart-error-ink text-xs" data-testid={testId}>
			{title}: {getBenchErrorMessage(error)}
		</p>
	);
}

/** Members of the organization with their roles; admins change roles, remove members and invite. */
export function MembersTab({ org }: { org: OrgSummary }) {
	const { t } = useTranslation("platform");
	const session = useSession();
	const members = useListMembersQuery(org.id);
	const [updateMember, updateState] = useUpdateMemberMutation();
	const [removeMember, removeState] = useRemoveMemberMutation();
	const isAdmin = canUse(session, "admin");
	const isOwner = session.role === "owner";
	const { t: ts } = useTranslation("accountSafety");
	const { t: tw } = useTranslation("w4b07");
	const [createResetLink, resetState] = useCreateResetLinkMutation();
	const [resetLink, setResetLink] = useState<ResetLink | null>(null);
	const onResetLink = async (userId: string) => {
		try {
			setResetLink(await createResetLink({ orgId: org.id, userId }).unwrap());
		} catch {
			// resetState.error renders the failure.
		}
	};

	const list = members.data?.members ?? [];
	return (
		<div className="flex flex-col gap-10" data-testid="settings-members">
			<SettingsSection
				title={t("settings.members.title")}
				description={t("settings.members.description", { org: org.name })}
				testId="settings-members-list"
			>
				{resetLink && (
					<OneTimeSecret
						title={ts("resetLink.created", { email: resetLink.email })}
						secret={resetLink.url}
						secretLabel={ts("resetLink.link")}
						onDone={() => setResetLink(null)}
						testId="reset-link-created"
					>
						<p className="text-muted-foreground text-xs">
							{ts("resetLink.hint", { email: resetLink.email, when: formatRelative(resetLink.expires_at, getLocale()) })}
						</p>
					</OneTimeSecret>
				)}
				{members.isLoading ? (
					<BlockSkeleton rows={4} testId="members-loading" />
				) : members.isError || !members.data ? (
					<LoadError
						title={t("settings.members.loadError")}
						error={members.error}
						onRetry={() => void members.refetch()}
						testId="members-error"
					/>
				) : list.length === 0 ? (
					<EmptyNote testId="members-empty">{t("settings.members.empty")}</EmptyNote>
				) : (
					<TableFrame testId="members-table">
						<Table>
							<TableHeader>
								<TableRow>
									{/* Wave 4, N33: only admins see the members' addresses. */}
									<TableHead>{isAdmin ? t("settings.members.email") : tw("members.person")}</TableHead>
									<TableHead>{t("settings.members.role")}</TableHead>
									<TableHead>{t("settings.members.since")}</TableHead>
									{isAdmin && <TableHead className="text-right">{t("settings.members.actions")}</TableHead>}
								</TableRow>
							</TableHeader>
							<TableBody>
								{list.map((member) => (
									<MemberRow
										key={member.user_id}
										member={member}
										org={org}
										isSelf={member.user_id === session.user?.id}
										isAdmin={isAdmin}
										callerRole={session.role}
										canReset={isOwner && member.user_id !== session.user?.id}
										onResetLink={() => void onResetLink(member.user_id)}
										resetBusy={resetState.isLoading && resetState.originalArgs?.userId === member.user_id}
										onRole={(role) => void updateMember({ orgId: org.id, userId: member.user_id, role })}
										onRemove={() => void removeMember({ orgId: org.id, userId: member.user_id })}
										busy={
											(updateState.isLoading && updateState.originalArgs?.userId === member.user_id) ||
											(removeState.isLoading && removeState.originalArgs?.userId === member.user_id)
										}
									/>
								))}
							</TableBody>
						</Table>
					</TableFrame>
				)}
				{updateState.error && (
					<WriteError error={updateState.error} title={t("settings.members.updateError")} testId="members-update-error" />
				)}
				{removeState.error && (
					<WriteError error={removeState.error} title={t("settings.members.removeError")} testId="members-remove-error" />
				)}
				{resetState.error && !resetLink && (
					<WriteError error={resetState.error} title={ts("resetLink.error")} testId="members-reset-error" />
				)}
			</SettingsSection>

			<RoleGate min="admin">
				<InviteSection org={org} />
			</RoleGate>
		</div>
	);
}

function MemberRow({
	member,
	org,
	isSelf,
	isAdmin,
	callerRole,
	onRole,
	onRemove,
	busy,
	canReset = false,
	onResetLink,
	resetBusy = false,
}: {
	member: Member;
	org: OrgSummary;
	isSelf: boolean;
	isAdmin: boolean;
	callerRole: Role | undefined;
	onRole: (role: Role) => void;
	onRemove: () => void;
	busy: boolean;
	canReset?: boolean;
	onResetLink?: () => void;
	resetBusy?: boolean;
}) {
	const { t } = useTranslation("platform");
	const { t: ts } = useTranslation("accountSafety");
	const { t: tw } = useTranslation("w4b07");
	// The caller manages a member only when it holds at least the member's role; never itself here.
	const manageable = isAdmin && !isSelf && mayManage(callerRole, member.role);
	return (
		<TableRow data-testid={`member-row-${member.user_id}`}>
			<TableCell className="max-w-80">
				<div className="flex min-w-0 flex-col">
					<span className="text-foreground flex items-center gap-2 truncate font-medium">
						{member.name || (isAdmin ? member.email : tw("members.someone"))}
						{isSelf && (
							<span className="bg-primary/10 text-primary rounded-sm px-1 py-px text-[10.5px] font-semibold tracking-wide uppercase">
								{t("settings.members.you")}
							</span>
						)}
					</span>
					{isAdmin && member.name && <span className="text-muted-foreground truncate font-mono text-xs">{member.email}</span>}
				</div>
			</TableCell>
			<TableCell>
				{manageable ? (
					<RoleSelect
						value={member.role}
						onChange={(role) => role !== member.role && onRole(role)}
						roles={grantableRoles(callerRole)}
						label={t("settings.members.roleLabel", { email: member.email })}
						disabled={busy}
						testId={`member-role-${member.user_id}`}
					/>
				) : (
					<RoleBadge role={member.role} />
				)}
			</TableCell>
			<TableCell className="text-muted-foreground">
				<When value={member.created_at} />
			</TableCell>
			{isAdmin && (
				<TableCell className="text-right">
					<div className="inline-flex items-center gap-2">
						{canReset && onResetLink && (
							<ConfirmButton
								label={ts("resetLink.button")}
								question={ts("resetLink.question", { email: member.email })}
								confirmLabel={ts("resetLink.confirm")}
								cancelLabel={ts("resetLink.cancel")}
								onConfirm={onResetLink}
								isLoading={resetBusy}
								testId={`member-reset-${member.user_id}`}
							/>
						)}
						{manageable && (
							<ConfirmButton
								label={t("settings.members.remove")}
								question={t("settings.members.confirmRemove", { email: member.email, org: org.name })}
								confirmLabel={t("settings.members.confirm")}
								cancelLabel={t("settings.members.cancel")}
								onConfirm={onRemove}
								isLoading={busy}
								testId={`member-remove-${member.user_id}`}
							/>
						)}
					</div>
				</TableCell>
			)}
		</TableRow>
	);
}

/** Invite by email with a role; the link is shown once. Then the open invitations, revocable. */
function InviteSection({ org }: { org: OrgSummary }) {
	const { t } = useTranslation("platform");
	const { t: h } = useTranslation("hw03");
	const session = useSession();
	const formId = useId();
	const roles = grantableRoles(session.role);
	const [email, setEmail] = useState("");
	const [role, setRole] = useState<Role>("member");
	const [touched, setTouched] = useState(false);
	const [created, setCreated] = useState<{ email: string; link: string } | null>(null);
	const [createInvitation, createState] = useCreateInvitationMutation();
	const invitations = useListInvitationsQuery(org.id);
	const [revoke, revokeState] = useRevokeInvitationMutation();

	const emailError = touched && !looksLikeEmail(email) ? t("settings.invite.invalidEmail") : undefined;
	const onSubmit = async (event: FormEvent) => {
		event.preventDefault();
		setTouched(true);
		if (!looksLikeEmail(email)) return;
		try {
			const result = await createInvitation({ orgId: org.id, email: email.trim(), role }).unwrap();
			// The server's link when it sends one (built from its public URL), else one on this site.
			setCreated({ email: result.email, link: result.url || (result.token ? invitationLink(pageOrigin(), result.token) : "") });
			setEmail("");
			setTouched(false);
		} catch {
			// createState.error renders the failure.
		}
	};

	const pending = (invitations.data?.invitations ?? []).filter((invite: Invitation) => !invite.accepted_at);
	return (
		<div className="flex flex-col gap-10">
			<SettingsSection title={t("settings.invite.title")} description={t("settings.invite.description")} testId="settings-invite">
				{created?.link ? (
					<OneTimeSecret
						title={t("settings.invite.created", { email: created.email })}
						secret={created.link}
						secretLabel={t("settings.invite.link")}
						onDone={() => setCreated(null)}
						testId="invite-created"
					>
						<p className="text-muted-foreground text-xs">{t("settings.invite.linkHint", { email: created.email })}</p>
					</OneTimeSecret>
				) : (
					<form
						className="bg-muted/30 flex flex-col gap-3 rounded-lg border p-4 sm:flex-row sm:items-end"
						onSubmit={(event) => void onSubmit(event)}
						noValidate
						data-testid="invite-form"
					>
						<div className="flex min-w-0 flex-1 flex-col gap-1.5">
							<label htmlFor={`${formId}-email`} className="text-[13px] font-medium">
								{t("settings.invite.email")}
							</label>
							<Input
								id={`${formId}-email`}
								type="email"
								autoComplete="off"
								placeholder={t("settings.invite.emailPlaceholder")}
								value={email}
								onChange={(event) => setEmail(event.target.value)}
								aria-invalid={Boolean(emailError) || undefined}
								data-testid="invite-email"
							/>
							{emailError && <span className="text-chart-error-ink text-xs">{emailError}</span>}
						</div>
						<div className="flex flex-col gap-1.5">
							<span className="text-[13px] font-medium">{t("settings.invite.role")}</span>
							<RoleSelect
								value={role}
								onChange={setRole}
								roles={roles}
								label={t("settings.invite.role")}
								testId="invite-role"
								size="default"
							/>
						</div>
						<Button type="submit" className="h-9 gap-1.5" isLoading={createState.isLoading} dataTestId="invite-submit">
							<UserPlus className="size-4" aria-hidden="true" />
							{t("settings.invite.submit")}
						</Button>
					</form>
				)}
				{createState.error && !created && <WriteError error={createState.error} title={t("settings.invite.error")} testId="invite-error" />}
			</SettingsSection>

			<SettingsSection title={t("settings.invite.pending")} testId="settings-invitations">
				{invitations.isLoading ? (
					<BlockSkeleton rows={2} testId="invitations-loading" />
				) : invitations.isError ? (
					<LoadError
						title={t("settings.invite.loadError")}
						error={invitations.error}
						onRetry={() => void invitations.refetch()}
						testId="invitations-error"
					/>
				) : pending.length === 0 ? (
					<EmptyNote testId="invitations-empty">{t("settings.invite.pendingEmpty")}</EmptyNote>
				) : (
					<TableFrame testId="invitations-table">
						<Table>
							<TableHeader>
								<TableRow>
									<TableHead>{t("settings.members.email")}</TableHead>
									<TableHead>{t("settings.members.role")}</TableHead>
									<TableHead>{t("settings.invite.expires")}</TableHead>
									<TableHead className="text-right">{t("settings.members.actions")}</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{pending.map((invite) => (
									<TableRow key={invite.id} data-testid={`invitation-row-${invite.id}`}>
										<TableCell className="font-mono text-xs">{invite.email}</TableCell>
										<TableCell>
											<RoleBadge role={invite.role} />
										</TableCell>
										<TableCell className="text-muted-foreground">
											<When value={invite.expires_at} />
										</TableCell>
										<TableCell className="text-right">
											<ConfirmButton
												label={t("settings.invite.revoke")}
												question={h("invite.revokeQuestion", { email: invite.email })}
												confirmLabel={h("invite.revokeConfirm")}
												cancelLabel={h("invite.revokeCancel")}
												variant="destructive"
												isLoading={revokeState.isLoading && revokeState.originalArgs?.invitationId === invite.id}
												onConfirm={() => void revoke({ orgId: org.id, invitationId: invite.id })}
												testId={`invitation-revoke-${invite.id}`}
											/>
										</TableCell>
									</TableRow>
								))}
							</TableBody>
						</Table>
					</TableFrame>
				)}
				{revokeState.error && (
					<WriteError error={revokeState.error} title={t("settings.invite.revokeError")} testId="invitation-revoke-error" />
				)}
			</SettingsSection>
		</div>
	);
}