import { AuthError, AuthHeading } from "@/components/platform/authForm";
import { Button } from "@/components/ui/button";
import { useSession } from "@/lib/session/sessionContext";
import { errorStatus } from "@/lib/store/apis/session";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useVerifyEmailMutation } from "@/lib/store/apis/identityApi";
import { Link } from "@tanstack/react-router";
import { CheckCircle2, MailCheck } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/**
 * The page a verification link opens (/verify-email?token=...). Nothing happens on load: the visitor
 * presses Confirm, which posts the token, so a mail scanner that fetches the link verifies nothing.
 * The link works without a session (it may be opened on another device).
 */
export default function VerifyEmailView({ token }: { token?: string }) {
	const { t } = useTranslation("identity");
	const session = useSession();
	const [verify, state] = useVerifyEmailMutation();
	const [email, setEmail] = useState<string>();

	const next = (variant: "default" | "outline") => (
		<Button asChild variant={variant} size="lg" className="h-10 w-full">
			{session.signedIn ? (
				<Link to="/" data-testid="verify-continue">
					{t("verify.continue")}
				</Link>
			) : (
				<Link to="/login" data-testid="verify-sign-in">
					{t("verify.signIn")}
				</Link>
			)}
		</Button>
	);

	if (!token) {
		return (
			<div className="flex flex-col gap-6" data-testid="verify-missing">
				<AuthHeading title={t("verify.missing")} subtitle={t("verify.missingBody")} />
				{next("outline")}
			</div>
		);
	}
	if (email) {
		return (
			<div className="flex flex-col gap-6" data-testid="verify-done">
				<div className="bg-chart-success/10 text-chart-success-ink flex size-11 items-center justify-center rounded-xl">
					<CheckCircle2 className="size-5" aria-hidden="true" />
				</div>
				<AuthHeading title={t("verify.done")} subtitle={t("verify.doneBody", { email })} />
				{next("default")}
			</div>
		);
	}
	if (state.isError && errorStatus(state.error) === 404) {
		return (
			<div className="flex flex-col gap-6" data-testid="verify-invalid">
				<AuthHeading title={t("verify.invalid")} subtitle={t("verify.invalidBody")} />
				{next("outline")}
			</div>
		);
	}

	const onConfirm = async () => {
		try {
			const result = await verify({ token }).unwrap();
			setEmail(result.email);
		} catch {
			// state.error renders the failure.
		}
	};

	return (
		<div className="flex flex-col gap-6" data-testid="verify-screen">
			<div className="bg-primary/10 text-primary flex size-11 items-center justify-center rounded-xl">
				<MailCheck className="size-5" aria-hidden="true" />
			</div>
			<AuthHeading title={t("verify.title")} subtitle={t("verify.subtitle")} />
			{state.isError && <AuthError title={t("verify.error")} message={getBenchErrorMessage(state.error)} testId="verify-error" />}
			<Button size="lg" className="h-10 w-full" isLoading={state.isLoading} onClick={() => void onConfirm()} dataTestId="verify-submit">
				{state.isLoading ? t("verify.confirming") : t("verify.confirm")}
			</Button>
			<p className="text-muted-foreground text-xs">{t("verify.note")}</p>
		</div>
	);
}
