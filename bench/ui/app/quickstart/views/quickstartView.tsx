import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { signupOffered, useSession, type SessionInfo } from "@/lib/session/sessionContext";
import { useGetSuitesQuery } from "@/lib/store";
import "@/lib/types/u19I18n";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { useTranslation } from "react-i18next";
import "./fw22I18n";
import { MinedTasks, QuickstartSteps } from "./quickstartCards";

/** A hosted visitor who is not signed in: the page shows the steps only, never org data. */
export function isSignedOutVisitor(session: SessionInfo): boolean {
	return session.authMode === "hosted" && session.status === "ready" && !session.signedIn;
}

/** Where a visitor goes to sign in and come back here. */
export const QUICKSTART_LOGIN_HREF = `/login?return_to=${encodeURIComponent("/quickstart")}`;

/** In place of the mined tasks for a signed-out visitor: sign in (or sign up) to start. */
export function QuickstartSignedOut({ signup }: { signup: boolean }) {
	const { t } = useTranslation("fw22");
	return (
		<div className="bg-card flex flex-col gap-3 rounded-xl border p-5" data-testid="quickstart-signed-out">
			<h2 className="text-base font-semibold">{t("quickstart.signedOutTitle")}</h2>
			<p className="text-muted-foreground text-sm">{t("quickstart.signedOutBody")}</p>
			<div className="flex flex-wrap gap-2">
				<Button asChild dataTestId="quickstart-sign-in">
					<a href={QUICKSTART_LOGIN_HREF}>{t("quickstart.signIn")}</a>
				</Button>
				{signup ? (
					<Button asChild variant="outline" dataTestId="quickstart-sign-up">
						<a href="/signup">{t("quickstart.signUp")}</a>
					</Button>
				) : null}
			</div>
		</div>
	);
}

/** Get started: /quickstart, linked from Home. The four commands as step cards, then the mined tasks that need review. */
export default function QuickstartView() {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("u19");
	const session = useSession();
	const signedOut = isSignedOutVisitor(session);
	// A visitor has no organization: the suites call would only answer 401.
	const { data, error, isLoading, refetch } = useGetSuitesQuery(signedOut ? skipToken : undefined);
	return (
		<section className="mx-auto flex w-full max-w-5xl min-w-0 flex-col gap-8" data-testid="quickstart-view">
			<PageHeader
				title={tu("quickstart.title")}
				subtitle={tu("quickstart.subtitle")}
				breadcrumbs={[{ label: t("home.title"), to: "/" }, { label: tu("quickstart.title") }]}
				testId="quickstart-header"
			/>
			<QuickstartSteps />
			{signedOut ? (
				<QuickstartSignedOut signup={signupOffered(session)} />
			) : isLoading ? (
				<BlockSkeleton testId="quickstart-loading" />
			) : error ? (
				<LoadError title={t("quickstart.loadError")} error={error} onRetry={refetch} testId="quickstart-error" />
			) : (
				<MinedTasks suites={data?.suites ?? []} />
			)}
		</section>
	);
}