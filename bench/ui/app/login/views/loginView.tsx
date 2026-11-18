import { AuthError, AuthField, AuthHeading, GithubButton, loginErrorMessage } from "@/components/platform/authForm";
import { Button } from "@/components/ui/button";
import { useSession } from "@/lib/session/sessionContext";
import { useLoginMutation } from "@/lib/store/apis/platformApi";
import { safeReturnTo } from "@/lib/store/apis/session";
import { Link } from "@tanstack/react-router";
import { Info } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

/** Full-page navigation after signing in, so every cached answer is fetched again as the new user. */
function go(path: string) {
	window.location.assign(path);
}

/**
 * Sign in with email and password, or GitHub when the server has it. Comes back to return_to (a
 * path on this site) afterwards. In local mode there is nothing to sign in to, and the page says so.
 */
export default function LoginView({ returnTo, oauthError }: { returnTo?: string; oauthError?: string }) {
	const { t } = useTranslation("platform");
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
				<p className="border-border bg-muted/40 text-muted-foreground flex items-start gap-2.5 rounded-md border px-3 py-3 text-sm">
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
		<div className="flex flex-col gap-7" data-testid="login-screen">
			<AuthHeading title={t("auth.login.title")} subtitle={t("auth.login.subtitle")} />
			<form className="flex flex-col gap-4" onSubmit={(event) => void onSubmit(event)} noValidate data-testid="login-form">
				<AuthField
					label={t("auth.email")}
					type="email"
					name="email"
					autoComplete="username"
					required
					autoFocus
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
					className="mt-1 h-10 w-full"
					isLoading={state.isLoading}
					disabled={!email.trim() || !password}
					dataTestId="login-submit"
				>
					{t("auth.login.submit")}
				</Button>
				{session.github && <GithubButton returnTo={target} />}
			</form>
			{session.signup === "open" && (
				<p className="text-muted-foreground text-sm">
					{t("auth.login.noAccount")}{" "}
					<Link to="/signup" className="text-primary font-medium underline-offset-4 hover:underline" data-testid="login-signup-link">
						{t("auth.login.signUp")}
					</Link>
				</p>
			)}
		</div>
	);
}