import { Button } from "@/components/ui/button";
import i18n from "@/lib/i18n";
import { Link, useRouter, type ErrorComponentProps } from "@tanstack/react-router";
import { TriangleAlert } from "lucide-react";
import { I18nextProvider, useTranslation } from "react-i18next";

/**
 * A page that threw while rendering. It shows inside the shell, so the navigation still works:
 * what failed, the error text, a retry and the way home.
 */
export function RouteErrorComponent({ error, reset }: Partial<ErrorComponentProps>) {
	const { t } = useTranslation("bench");
	const router = useRouter();
	const message = error instanceof Error ? error.message : String(error ?? "");
	const retry = () => {
		reset?.();
		void router.invalidate();
	};
	return (
		<section role="alert" className="flex max-w-2xl flex-col items-start gap-3" data-testid="route-error">
			<span className="bg-chart-error/10 text-chart-error-ink flex size-9 items-center justify-center rounded-full">
				<TriangleAlert className="size-4.5" aria-hidden="true" />
			</span>
			<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("shell.error.title")}</h1>
			<p className="text-muted-foreground text-sm">{t("shell.error.description")}</p>
			{message && (
				<pre className="bg-muted/50 text-muted-foreground w-full overflow-x-auto rounded-md border px-3 py-2 font-mono text-xs whitespace-pre-wrap">
					{message}
				</pre>
			)}
			<div className="flex flex-wrap gap-2">
				<Button size="sm" onClick={retry} dataTestId="route-error-retry">
					{t("shell.error.retry")}
				</Button>
				<Button asChild size="sm" variant="outline">
					<Link to="/" data-testid="route-error-home">
						{t("shell.backHome")}
					</Link>
				</Button>
			</div>
		</section>
	);
}

/** The root's own failure (the shell itself threw): the same message without the shell. */
export function RootErrorComponent(props: Partial<ErrorComponentProps>) {
	return (
		<I18nextProvider i18n={i18n}>
			<main className="bg-background flex min-h-dvh items-center justify-center p-6">
				<RouteErrorComponent {...props} />
			</main>
		</I18nextProvider>
	);
}