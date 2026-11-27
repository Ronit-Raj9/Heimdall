import { AuthError, AuthField } from "@/components/platform/authForm";
import { Button } from "@/components/ui/button";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useChangePasswordMutation } from "@/lib/store/apis/platformApi";
import { Check } from "lucide-react";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { SettingsSection } from "./settingsBits";
import { useUnsavedGuard } from "./useUnsavedGuard";

/** Passwords shorter than this are refused here before the server refuses them (as on sign-up). */
const MIN_PASSWORD = 12;

/**
 * Change the signed-in user's password (POST /auth/password, session and CSRF). The server ends
 * every other session of the user and keeps this one; API tokens are not affected.
 */
export function PasswordTab() {
	const { t } = useTranslation("platform");
	const [current, setCurrent] = useState("");
	const [next, setNext] = useState("");
	const [repeat, setRepeat] = useState("");
	const [touched, setTouched] = useState(false);
	const [done, setDone] = useState(false);
	const [change, state] = useChangePasswordMutation();

	const tooShort = touched && next.length < MIN_PASSWORD;
	const mismatch = touched && repeat !== next;
	useUnsavedGuard(Boolean(current || next || repeat) && !state.isLoading);

	const onSubmit = async (event: FormEvent) => {
		event.preventDefault();
		setTouched(true);
		setDone(false);
		if (next.length < MIN_PASSWORD || repeat !== next) return;
		try {
			await change({ current_password: current, new_password: next }).unwrap();
			setCurrent("");
			setNext("");
			setRepeat("");
			setTouched(false);
			setDone(true);
		} catch {
			// state.error renders the failure.
		}
	};

	return (
		<SettingsSection title={t("settings.password.title")} description={t("settings.password.description")} testId="settings-password">
			<form className="flex max-w-md flex-col gap-4" onSubmit={(event) => void onSubmit(event)} noValidate data-testid="password-form">
				<AuthField
					label={t("settings.password.current")}
					hint={t("settings.password.currentHint")}
					type="password"
					autoComplete="current-password"
					value={current}
					onChange={(event) => setCurrent(event.target.value)}
					testId="password-current"
				/>
				<AuthField
					label={t("settings.password.new")}
					hint={t("settings.password.newHint", { min: MIN_PASSWORD })}
					error={tooShort ? t("settings.password.tooShort", { min: MIN_PASSWORD }) : undefined}
					type="password"
					autoComplete="new-password"
					value={next}
					onChange={(event) => setNext(event.target.value)}
					testId="password-new"
				/>
				<AuthField
					label={t("settings.password.confirm")}
					error={mismatch ? t("settings.password.mismatch") : undefined}
					type="password"
					autoComplete="new-password"
					value={repeat}
					onChange={(event) => setRepeat(event.target.value)}
					testId="password-repeat"
				/>
				{state.error && (
					<AuthError title={t("settings.password.error")} message={getBenchErrorMessage(state.error)} testId="password-error" />
				)}
				{done && (
					<p role="status" className="text-muted-foreground flex items-center gap-1.5 text-xs" data-testid="password-done">
						<Check className="size-3.5" aria-hidden="true" />
						{t("settings.password.done")}
					</p>
				)}
				<div>
					<Button type="submit" isLoading={state.isLoading} dataTestId="password-submit">
						{t("settings.password.submit")}
					</Button>
				</div>
			</form>
		</SettingsSection>
	);
}