import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { signupOffered, useSession, type SessionInfo } from "@/lib/session/sessionContext";
import { useGetSuitesQuery } from "@/lib/store";
import "@/lib/types/u19I18n";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { Link } from "@tanstack/react-router";
import { ArrowRight } from "lucide-react";
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

/** The website path to a first result, for people who do not want a terminal (M2-17). */
export function QuickstartWeb() {
	const { t } = useTranslation("u19");
	return (
		<div className="bg-card flex flex-col gap-4 rounded-xl border p-5 sm:flex-row sm:items-center" data-testid="quickstart-web">
			<div className="flex min-w-0 flex-1 flex-col gap-1">
				<h2 className="text-base font-semibold">{t("quickstart.web.title")}</h2>
				<p className="text-muted-foreground text-sm">{t("quickstart.web.body")}</p>
			</div>
			<div className="flex shrink-0 flex-wrap items-center gap-3">
				<Button asChild dataTestId="quickstart-web-evaluate">
					<Link to={"/evaluate" as never}>
						{t("quickstart.web.evaluate")}
						<ArrowRight aria-hidden="true" />
					</Link>
				</Button>
				<Link
					to={"/settings" as never}
					search={{ tab: "keys" } as never}
					className="text-foreground text-sm font-medium underline-offset-4 hover:underline"
					data-testid="quickstart-web-keys"
				>
					{t("quickstart.web.keys")}
				</Link>
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
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-8" data-testid="quickstart-view">
			<PageHeader
				title={tu("quickstart.title")}
				subtitle={tu("quickstart.subtitle")}
				breadcrumbs={[{ label: t("home.title"), to: "/" }, { label: tu("quickstart.title") }]}
				testId="quickstart-header"
			/>
			<QuickstartWeb />
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