import { AuthError, AuthField, AuthHeading, loginErrorMessage } from "@/components/platform/authForm";
import { Button } from "@/components/ui/button";
import { signupOffered, useSession } from "@/lib/session/sessionContext";
import { useLoginMutation } from "@/lib/store/apis/platformApi";
import { safeReturnTo } from "@/lib/store/apis/session";
import { Link } from "@tanstack/react-router";
import { Info } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { GithubPrimary, OrDivider } from "./authParts";

/** Full-page navigation after signing in, so every cached answer is fetched again as the new user. */
function go(path: string) {
	window.location.assign(path);
}

/**
 * Sign in with email and password, or GitHub when the server has it. Comes back to return_to (a
 * path on this site) afterwards. In local mode there is nothing to sign in to, and the page says so.
 */
export default function LoginView({
	returnTo,
	oauthError,
	expired,
}: {
	returnTo?: string;
	oauthError?: string;
	/** The tab was signed in before: say the session ended (X-96). */
	expired?: boolean;
}) {
	const { t } = useTranslation("platform");
	const { t: tu } = useTranslation("u19");
	const { t: ts2 } = useTranslation("ufs2");
	const session = useSession();
	const target = safeReturnTo(returnTo);
	const [email, setEmail] = useState("");
	const [password, setPassword] = useState("");
	const [login, state] = useLoginMutation();
	// Already signed in (a second tab, the back button): carry on to where the visitor was going.
	// target is a same-site path (safeReturnTo); a full replace, like go(), needs no typed route `to`.
	useEffect(() => {
		if (session.signedIn) window.location.replace(target); // build fix requested by lead (P6 agent)
	}, [session.signedIn, target]);

	if (session.status === "ready" && session.authMode === "local") {
		return (
			<div className="flex flex-col gap-6" data-testid="login-local">
				<AuthHeading title={t("auth.login.title")} />
				<p className="bg-muted/50 text-muted-foreground flex items-start gap-2.5 rounded-xl px-3.5 py-3 text-sm">
					<Info className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("auth.login.localMode")}
				</p>
				<Button asChild className="h-10 w-full">
					<Link to="/">{t("auth.login.openApp")}</Link>
				</Button>
			</div>
		);
	}

	const onSubmit = async (event: FormEvent) => {
		event.preventDefault();
		try {
			await login({ email: email.trim(), password }).unwrap();
			go(target);
		} catch {
			// state.error renders the failure.
		}
	};

	return (
		<div className="flex flex-col gap-6" data-testid="login-screen">
			<AuthHeading title={tu("login.title")} />
			{expired && (
				<p className="bg-muted/50 text-foreground flex items-start gap-2.5 rounded-xl px-3.5 py-3 text-sm" role="status" data-testid="login-expired">
					<Info className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{ts2("login.expired", { page: target })}
				</p>
			)}
			{session.github && (
				<div className="flex flex-col gap-5">
					<GithubPrimary returnTo={target} />
					<OrDivider label={tu("login.orEmail")} />
				</div>
			)}
			<form className="flex flex-col gap-4" onSubmit={(event) => void onSubmit(event)} noValidate data-testid="login-form">
				<AuthField
					label={t("auth.email")}
					type="email"
					name="email"
					autoComplete="username"
					required
					autoFocus={!session.github}
					value={email}
					onChange={(event) => setEmail(event.target.value)}
					testId="login-email"
				/>
				<AuthField
					label={t("auth.password")}
					type="password"
					name="password"
					autoComplete="current-password"
					required
					value={password}
					onChange={(event) => setPassword(event.target.value)}
					testId="login-password"
				/>
				{state.error && <AuthError title={t("auth.login.error")} message={loginErrorMessage(state.error, t)} testId="login-error" />}
				{!state.error && oauthError && (
					<AuthError
						title={t("auth.login.error")}
						message={oauthError === "signup_not_allowed" ? t("auth.login.githubNoAccount") : t("auth.login.githubFailed")}
						testId="login-oauth-error"
					/>
				)}
				<Button
					type="submit"
					size="lg"
					variant={session.github ? "outline" : "default"}
					className="mt-1 h-10 w-full"
					isLoading={state.isLoading}
					disabled={!email.trim() || !password}
					dataTestId="login-submit"
				>
					{t("auth.login.submit")}
				</Button>
			</form>
			{signupOffered(session) && (
				<p className="text-muted-foreground text-center text-sm">
					{t("auth.login.noAccount")}{" "}
					<Link to="/signup" className="text-primary font-medium underline-offset-4 hover:underline" data-testid="login-signup-link">
						{t("auth.login.signUp")}
					</Link>
				</p>
			)}
		</div>
	);
}
