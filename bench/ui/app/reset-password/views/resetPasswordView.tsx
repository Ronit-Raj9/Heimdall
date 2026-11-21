import { AuthError, AuthField, AuthHeading } from "@/components/platform/authForm";
import { formatRelative } from "@/components/platform/platform.utils";
import { BlockSkeleton } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { getLocale } from "@/lib/i18n";
import { useGetResetQuery, useResetPasswordMutation } from "@/lib/store/apis/accountSafetyApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { Link } from "@tanstack/react-router";
import { CheckCircle2, KeyRound } from "lucide-react";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

export const MIN_PASSWORD = 12;

/**
 * The page a one-time reset link opens (/reset-password?token=...). Shows which account the link
 * resets, takes the new password twice and sets it. The link works once; every session ends.
 */
export default function ResetPasswordView({ token }: { token?: string }) {
	const { t } = useTranslation("accountSafety");
	const info = useGetResetQuery(token ?? "", { skip: !token });
	const [reset, state] = useResetPasswordMutation();
	const [password, setPassword] = useState("");
	const [repeat, setRepeat] = useState("");
	const [touched, setTouched] = useState(false);
	const [done, setDone] = useState(false);

	const signInButton = (variant: "default" | "outline") => (
		<Button asChild variant={variant} size="lg" className="h-10 w-full">
			<Link to="/login" data-testid="reset-sign-in">
				{t("reset.signIn")}
			</Link>
		</Button>
	);

	if (!token) {
		return (
			<div className="flex flex-col gap-6" data-testid="reset-missing">
				<AuthHeading title={t("reset.missing")} subtitle={t("reset.missingBody")} />
				{signInButton("outline")}
			</div>
		);
	}
	if (done) {
		return (
			<div className="flex flex-col gap-6" data-testid="reset-done">
				<div className="bg-chart-success/10 text-chart-success-ink flex size-11 items-center justify-center rounded-xl">
					<CheckCircle2 className="size-5" aria-hidden="true" />
				</div>
				<AuthHeading title={t("reset.done")} subtitle={t("reset.doneBody")} />
				{signInButton("default")}
			</div>
		);
	}
	if (info.isLoading) {
		return (
			<div className="flex flex-col gap-6" aria-label={t("reset.loading")}>
				<AuthHeading title={t("reset.title")} />
				<BlockSkeleton rows={3} testId="reset-loading" />
			</div>
		);
	}
	if (info.isError || !info.data) {
		return (
			<div className="flex flex-col gap-6" data-testid="reset-invalid">
				<AuthHeading title={t("reset.invalid")} subtitle={t("reset.invalidBody")} />
				{signInButton("outline")}
			</div>
		);
	}

	const tooShort = touched && password.length < MIN_PASSWORD;
	const mismatch = touched && repeat !== password;
	const expires = formatRelative(info.data.expires_at, getLocale());
	const onSubmit = async (event: FormEvent) => {
		event.preventDefault();
		setTouched(true);
		if (password.length < MIN_PASSWORD || repeat !== password) return;
		try {
			await reset({ token, password }).unwrap();
			setDone(true);
		} catch {
			// state.error renders the failure.
		}
	};

	return (
		<div className="flex flex-col gap-7" data-testid="reset-screen">
			<div className="bg-primary/10 text-primary flex size-11 items-center justify-center rounded-xl">
				<KeyRound className="size-5" aria-hidden="true" />
			</div>
			<AuthHeading title={t("reset.title")} subtitle={t("reset.subtitle", { email: info.data.email })} />
			<form className="flex flex-col gap-4" onSubmit={(event) => void onSubmit(event)} noValidate data-testid="reset-form">
				<input type="email" name="email" autoComplete="username" value={info.data.email} readOnly hidden />
				<AuthField
					label={t("reset.password")}
					type="password"
					name="new-password"
					autoComplete="new-password"
					required
					autoFocus
					minLength={MIN_PASSWORD}
					value={password}
					onChange={(event) => setPassword(event.target.value)}
					hint={t("reset.hint")}
					error={tooShort ? t("reset.hint") : undefined}
					testId="reset-password"
				/>
				<AuthField
					label={t("reset.confirm")}
					type="password"
					name="confirm-password"
					autoComplete="new-password"
					required
					value={repeat}
					onChange={(event) => setRepeat(event.target.value)}
					error={mismatch && !tooShort ? t("reset.mismatch") : undefined}
					testId="reset-repeat"
				/>
				{state.error && <AuthError title={t("reset.error")} message={getBenchErrorMessage(state.error)} testId="reset-error" />}
				<Button type="submit" size="lg" className="mt-1 h-10 w-full" isLoading={state.isLoading} dataTestId="reset-submit">
					{t("reset.submit")}
				</Button>
				{expires && <p className="text-muted-foreground text-xs">{t("reset.expires", { when: expires })}</p>}
			</form>
		</div>
	);
}
