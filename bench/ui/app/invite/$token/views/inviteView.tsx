import { AuthError, AuthHeading } from "@/components/platform/authForm";
import { RoleBadge } from "@/components/platform/badges";
import { formatRelative } from "@/components/platform/platform.utils";
import { BlockSkeleton } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { getLocale } from "@/lib/i18n";
import { useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useAcceptInvitationMutation, useGetInvitationQuery, useLogoutMutation } from "@/lib/store/apis/platformApi";
import { Link } from "@tanstack/react-router";
import { MailCheck } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * An invitation link. Shows what it grants; a signed-in user with the invited email accepts it, a
 * visitor signs in or creates an account first and comes back here.
 */
export default function InviteView({ token }: { token: string }) {
	const { t } = useTranslation("platform");
	const session = useSession();
	const invite = useGetInvitationQuery(token);
	const [accept, acceptState] = useAcceptInvitationMutation();
	const [logout] = useLogoutMutation();
	const here = `/invite/${encodeURIComponent(token)}`;

	if (invite.isLoading) {
		return (
			<div className="flex flex-col gap-6" aria-label={t("auth.invite.loading")}>
				<AuthHeading title={t("auth.invite.title")} />
				<BlockSkeleton rows={3} testId="invite-loading" />
			</div>
		);
	}
	if (invite.isError || !invite.data) {
		return (
			<div className="flex flex-col gap-6" data-testid="invite-invalid">
				<AuthHeading title={t("auth.invite.invalid")} subtitle={t("auth.invite.invalidBody")} />
				<Button asChild variant="outline" className="h-10 w-full">
					<Link to="/login">{t("auth.signup.signIn")}</Link>
				</Button>
			</div>
		);
	}

	const data = invite.data;
	const role = t(`roles.${data.role}`).toLowerCase();
	const expires = formatRelative(data.expires_at, getLocale());
	const sameEmail = session.user?.email?.toLowerCase() === data.email.toLowerCase();

	const onAccept = async () => {
		try {
			await accept(token).unwrap();
			window.location.assign("/");
		} catch {
			// acceptState.error renders the failure.
		}
	};
	const onSwitchAccount = async () => {
		try {
			await logout().unwrap();
		} catch {
			// Fall through to the login page either way.
		}
		window.location.assign(`/login?return_to=${encodeURIComponent(here)}`);
	};

	return (
		<div className="flex flex-col gap-7" data-testid="invite-screen">
			<div className="bg-primary/10 text-primary flex size-11 items-center justify-center rounded-xl">
				<MailCheck className="size-5" aria-hidden="true" />
			</div>
			<AuthHeading title={t("auth.invite.title")} subtitle={t("auth.invite.body", { email: data.email, org: data.org_name, role })} />
			<div className="flex flex-col gap-3 rounded-lg border p-4" data-testid="invite-card">
				<div className="flex items-center gap-3">
					<span className="bg-foreground text-background flex size-9 items-center justify-center rounded-lg text-sm font-semibold">
						{data.org_name.charAt(0).toUpperCase()}
					</span>
					<div className="flex min-w-0 flex-col">
						<span className="text-foreground truncate font-medium">{data.org_name}</span>
						<span className="text-muted-foreground truncate font-mono text-xs">{data.org_slug}</span>
					</div>
					<RoleBadge role={data.role} className="ml-auto" />
				</div>
				{expires && <p className="text-muted-foreground text-xs">{t("auth.invite.expires", { when: expires })}</p>}
			</div>

			{session.signedIn && sameEmail ? (
				<div className="flex flex-col gap-3">
					<p className="text-muted-foreground text-sm">{t("auth.invite.signedInAs", { email: session.user?.email ?? "" })}</p>
					{acceptState.error && (
						<AuthError
							title={t("auth.invite.acceptError")}
							message={getBenchErrorMessage(acceptState.error)}
							testId="invite-accept-error"
						/>
					)}
					<Button
						size="lg"
						className="h-10 w-full"
						onClick={() => void onAccept()}
						isLoading={acceptState.isLoading}
						dataTestId="invite-accept"
					>
						{t("auth.invite.accept", { org: data.org_name })}
					</Button>
				</div>
			) : session.signedIn ? (
				<div className="flex flex-col gap-3" data-testid="invite-wrong-account">
					<p className="border-chart-warning/50 bg-chart-warning/10 text-foreground rounded-md border px-3 py-2.5 text-sm">
						{t("auth.invite.wrongAccount", { email: data.email })}
					</p>
					<Button variant="outline" className="h-10 w-full" onClick={() => void onSwitchAccount()} dataTestId="invite-switch-account">
						{t("header.signOut")}
					</Button>
				</div>
			) : (
				<div className="flex flex-col gap-3" data-testid="invite-signed-out">
					<p className="text-muted-foreground text-sm">{t("auth.invite.signInFirst", { email: data.email })}</p>
					{!data.account_exists && data.signup_allowed && (
						<Button asChild size="lg" className="h-10 w-full">
							<Link to="/signup" search={{ invitation: token }} data-testid="invite-create-account">
								{t("auth.invite.createAccount")}
							</Link>
						</Button>
					)}
					{!data.account_exists && !data.signup_allowed && (
						<p className="text-muted-foreground text-xs" data-testid="invite-signup-closed">
							{t("auth.invite.signupClosed")}
						</p>
					)}
					<Button asChild size="lg" variant={data.account_exists || !data.signup_allowed ? "default" : "outline"} className="h-10 w-full">
						<Link to="/login" search={{ return_to: here }} data-testid="invite-sign-in">
							{t("auth.invite.signInToAccept")}
						</Link>
					</Button>
				</div>
			)}
		</div>
	);
}