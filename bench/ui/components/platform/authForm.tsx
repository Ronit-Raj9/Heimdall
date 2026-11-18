import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { githubStartUrl } from "@/lib/store/apis/platformApi";
import { cn } from "@/lib/utils";
import { AlertTriangle, Github } from "lucide-react";
import { useId, type InputHTMLAttributes, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** Title and one line under it, at the top of an auth card. */
export function AuthHeading({ title, subtitle }: { title: string; subtitle?: ReactNode }) {
	return (
		<div className="flex flex-col gap-1.5">
			<h1 className="text-foreground text-[1.6rem] leading-tight font-semibold tracking-[-0.022em] text-balance">{title}</h1>
			{subtitle && <p className="text-muted-foreground text-sm text-pretty">{subtitle}</p>}
		</div>
	);
}

export interface AuthFieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "id"> {
	label: string;
	hint?: string;
	error?: string;
	testId: string;
}

/** A labelled input with an optional hint and error, wired for screen readers. */
export function AuthField({ label, hint, error, testId, className, ...input }: AuthFieldProps) {
	const id = useId();
	const hintId = hint ? `${id}-hint` : undefined;
	const errorId = error ? `${id}-error` : undefined;
	return (
		<div className={cn("flex flex-col gap-1.5", className)}>
			<label htmlFor={id} className="text-foreground text-[13px] font-medium">
				{label}
			</label>
			<Input
				id={id}
				aria-describedby={[hintId, errorId].filter(Boolean).join(" ") || undefined}
				aria-invalid={Boolean(error) || undefined}
				className="h-10"
				data-testid={testId}
				{...input}
			/>
			{hint && !error && (
				<p id={hintId} className="text-muted-foreground text-xs">
					{hint}
				</p>
			)}
			{error && (
				<p id={errorId} className="text-chart-error-ink text-xs" data-testid={`${testId}-error`}>
					{error}
				</p>
			)}
		</div>
	);
}

/** A failed auth request: a title and the reason, in words the visitor can act on. */
export function AuthError({ title, message, testId }: { title: string; message: string; testId: string }) {
	return (
		<div
			role="alert"
			className="border-chart-error/40 bg-chart-error/5 flex items-start gap-2.5 rounded-md border px-3 py-2.5 text-sm"
			data-testid={testId}
		>
			<AlertTriangle className="text-chart-error-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
			<div className="flex min-w-0 flex-col gap-0.5">
				<span className="text-foreground font-medium">{title}</span>
				<span className="text-muted-foreground break-words">{message}</span>
			</div>
		</div>
	);
}

/** The message for a failed login: wrong credentials and rate limits get their own words. */
export function loginErrorMessage(error: unknown, t: ReturnType<typeof useTranslation<"platform">>["t"]): string {
	const status = error && typeof error === "object" && "status" in error ? (error as { status: unknown }).status : undefined;
	if (status === 401) return t("auth.login.badCredentials");
	if (status === 429) return t("auth.login.rateLimited");
	return getBenchErrorMessage(error);
}

/** "Continue with GitHub", a full-page navigation to the server's OAuth start. */
export function GithubButton({ returnTo }: { returnTo: string }) {
	const { t } = useTranslation("platform");
	return (
		<>
			<div className="text-muted-foreground flex items-center gap-3 text-xs" aria-hidden="true">
				<span className="bg-border h-px flex-1" />
				{t("auth.or")}
				<span className="bg-border h-px flex-1" />
			</div>
			<Button asChild variant="outline" size="lg" className="h-10 w-full gap-2 px-4">
				<a href={githubStartUrl(returnTo)} data-testid="auth-github">
					<Github className="size-4" aria-hidden="true" />
					{t("auth.github")}
				</a>
			</Button>
		</>
	);
}