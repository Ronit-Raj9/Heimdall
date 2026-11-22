import { AuthError, AuthField, AuthHeading } from "@/components/platform/authForm";
import { GithubPrimary, OrDivider } from "@/app/login/views/authParts";
import { RoleBadge } from "@/components/platform/badges";
import { HCaptcha, useCaptchaConfig } from "@/components/platform/hcaptcha";
import { looksLikeEmail } from "@/components/platform/platform.utils";
import { BlockSkeleton } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { signupOffered, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useGetAuthConfigQuery, useGetInvitationQuery, useSignupMutation } from "@/lib/store/apis/platformApi";
import { Link, Navigate } from "@tanstack/react-router";
import { Info } from "lucide-react";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

/** Passwords shorter than this are refused here before the server refuses them. */
export const MIN_PASSWORD = 12;

/**
 * Create an account. Open sign-up creates a first organization with the new user as its owner; an
 * invitation token joins the inviting organization instead. Invite-only and closed servers say so.
 */
export default function SignupView({ invitation }: { invitation?: string }) {
	const { t } = useTranslation("platform");
	const { t: tu } = useTranslation("u19");
	const session = useSession();
	const invite = useGetInvitationQuery(invitation ?? "", { skip: !invitation });
	const [name, setName] = useState("");
	const [email, setEmail] = useState("");
	const [password, setPassword] = useState("");
	const [touched, setTouched] = useState(false);
	// Account safety (L22): hCaptcha on self-serve sign-up when the server asks for it.
	const captcha = useCaptchaConfig();
	const { t: tCaptcha } = useTranslation("accountSafety");
	const [captchaToken, setCaptchaToken] = useState("");
	const [signup, state] = useSignupMutation();
	// Wave A: the allowlist hint and how the verification link arrives.
	const { t: ti } = useTranslation("identity");
	const authConfig = useGetAuthConfigQuery(undefined, { skip: session.authMode === "local" });
	const delivery = authConfig.data?.email_delivery;
	// An invitation fixes the email: the account must be the one invited.
	const invitedEmail = invite.data?.email;

	if (session.signedIn) return <Navigate to="/" replace />;
	if (session.status === "ready" && session.authMode === "local") return <Navigate to="/" replace />;

	const notice = (text: string, testId: string) => (
		<div className="flex flex-col gap-6" data-testid={testId}>
			<AuthHeading title={t("auth.signup.title")} />
			<p className="bg-muted/50 text-muted-foreground flex items-start gap-2.5 rounded-xl px-3.5 py-3 text-sm">
				<Info className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
				{text}
			</p>
			<p className="text-muted-foreground text-center text-sm">
				{t("auth.signup.haveAccount")}{" "}
				<Link to="/login" className="text-primary font-medium underline-offset-4 hover:underline">
					{t("auth.signup.signIn")}
				</Link>
			</p>
		</div>
	);
	if (!invitation && session.signup === "closed" && !session.bootstrap) return notice(t("auth.signup.closed"), "signup-closed");
	if (!invitation && !signupOffered(session)) return notice(t("auth.signup.inviteOnly"), "signup-invite-only");
	if (invitation && invite.isLoading) return <BlockSkeleton rows={4} testId="signup-invite-loading" />;
	if (invitation && (invite.isError || !invite.data)) {
		return (
			<div className="flex flex-col gap-6" data-testid="signup-invite-invalid">
				<AuthHeading title={t("auth.invite.invalid")} subtitle={t("auth.invite.invalidBody")} />
			</div>
		);
	}

	const address = invitedEmail ?? email;
	const emailError = touched && !looksLikeEmail(address) ? t("settings.invite.invalidEmail") : undefined;
	const passwordShort = touched && password.length < MIN_PASSWORD;
	const needCaptcha = captcha.captcha && !invitation;
	const onSubmit = async (event: FormEvent) => {
		event.preventDefault();
		setTouched(true);
		if (!looksLikeEmail(address) || password.length < MIN_PASSWORD) return;
		if (needCaptcha && !captchaToken) return;
		try {
			const body = Object.assign(
				{ email: address.trim(), password, name: name.trim() || undefined, invitation },
				needCaptcha ? { captcha: captchaToken } : {},
			);
			await signup(body).unwrap();
			window.location.assign("/");
		} catch {
			// state.error renders the failure.
		}
	};

	const allowlist = !invitation && !session.bootstrap && session.signup === "allowlist" ? (session.signupDomains ?? []).join(", ") : "";
	const subtitle = invite.data
		? t("auth.signup.subtitleInvite", { org: invite.data.org_name, role: t(`roles.${invite.data.role}`).toLowerCase() })
		: t("auth.signup.subtitle");
	return (
		<div className="flex flex-col gap-6" data-testid="signup-screen">
			<AuthHeading title={t("auth.signup.title")} subtitle={subtitle} />
			{session.github && !invitation && (
				<div className="flex flex-col gap-5">
					<GithubPrimary returnTo="/" />
					<OrDivider label={tu("signup.orEmail")} />
				</div>
			)}
			{invite.data && (
				<div className="bg-muted/50 flex items-center gap-2 rounded-xl px-3.5 py-2.5 text-sm" data-testid="signup-invite-org">
					<span className="text-foreground truncate font-medium">{invite.data.org_name}</span>
					<RoleBadge role={invite.data.role} className="ml-auto" />
				</div>
			)}
			{allowlist && (
				<p className="bg-muted/50 text-muted-foreground flex items-start gap-2.5 rounded-xl px-3.5 py-3 text-sm" data-testid="signup-allowlist">
					<Info className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{ti("signup.allowlistOnly", { domains: allowlist })}
				</p>
			)}
			<form className="flex flex-col gap-4" onSubmit={(event) => void onSubmit(event)} noValidate data-testid="signup-form">
				<AuthField
					label={t("auth.name")}
					name="name"
					autoComplete="name"
					placeholder={t("auth.namePlaceholder")}
					value={name}
					onChange={(event) => setName(event.target.value)}
					testId="signup-name"
				/>
				<AuthField
					label={t("auth.email")}
					type="email"
					name="email"
					autoComplete="email"
					required
					readOnly={Boolean(invitedEmail)}
					value={address}
					onChange={(event) => setEmail(event.target.value)}
					error={emailError}
					testId="signup-email"
				/>
				<AuthField
					label={t("auth.password")}
					type="password"
					name="new-password"
					autoComplete="new-password"
					required
					minLength={MIN_PASSWORD}
					value={password}
					onChange={(event) => setPassword(event.target.value)}
					hint={t("auth.passwordHint")}
					error={passwordShort ? t("auth.passwordHint") : undefined}
					testId="signup-password"
				/>
				{needCaptcha && <HCaptcha siteKey={captcha.captcha_site_key} onToken={setCaptchaToken} testId="signup-captcha" />}
				{needCaptcha && touched && !captchaToken && (
					<p role="alert" className="text-chart-error-ink text-xs" data-testid="signup-captcha-required">
						{tCaptcha("captcha.required")}
					</p>
				)}
				{state.error && <AuthError title={t("auth.signup.error")} message={getBenchErrorMessage(state.error)} testId="signup-error" />}
				<Button
					type="submit"
					size="lg"
					variant={session.github && !invitation ? "outline" : "default"}
					className="mt-1 h-10 w-full"
					isLoading={state.isLoading}
					dataTestId="signup-submit"
				>
					{t("auth.signup.submit")}
				</Button>
				{delivery && (
					<p className="text-muted-foreground text-xs" data-testid="signup-verify-note">
						{delivery === "log" ? ti("signup.verifyLog") : ti("signup.verifyEmail")}
					</p>
				)}
			</form>
			<p className="text-muted-foreground text-center text-sm">
				{t("auth.signup.haveAccount")}{" "}
				<Link
					to="/login"
					search={invitation ? { return_to: `/invite/${encodeURIComponent(invitation)}` } : {}}
					className="text-primary font-medium underline-offset-4 hover:underline"
				>
					{t("auth.signup.signIn")}
				</Link>
			</p>
		</div>
	);
}