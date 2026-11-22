import { Button } from "@/components/ui/button";
import { needsEmailVerification, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useResendVerificationMutation } from "@/lib/store/apis/identityApi";
import { useGetAuthConfigQuery } from "@/lib/store/apis/platformApi";
import type { EmailDelivery } from "@/lib/types/bench";
import { MailWarning } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * Shown at the top of every page while the signed-in user's email is unverified (auth/verify.go).
 * Says what stays locked, and sends a new link. With no mail sender on the server the link goes to
 * the server log, so the copy points at the server admin instead of an inbox.
 */
export function VerifyEmailBanner() {
	const session = useSession();
	if (!needsEmailVerification(session)) return null;
	return <Banner email={session.user?.email ?? ""} />;
}

function Banner({ email }: { email: string }) {
	const { t } = useTranslation("identity");
	const config = useGetAuthConfigQuery();
	const [resend, state] = useResendVerificationMutation();
	const delivery: EmailDelivery = state.data?.delivery ?? config.data?.email_delivery ?? "email";
	let sent: string | undefined;
	if (state.data?.already_verified) sent = t("banner.already");
	else if (state.data) sent = state.data.delivery === "log" ? t("banner.sentLog") : t("banner.sentEmail", { email: state.data.email });
	return (
		<section
			role="status"
			className="border-chart-warning/50 bg-chart-warning/10 mb-6 flex flex-col gap-3 rounded-xl border px-4 py-3 sm:flex-row sm:items-center sm:justify-between"
			data-testid="verify-banner"
			data-delivery={delivery}
		>
			<div className="flex min-w-0 items-start gap-3">
				<MailWarning className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
				<div className="min-w-0 text-sm">
					<p className="text-foreground font-medium">{t("banner.title")}</p>
					<p className="text-muted-foreground">{delivery === "log" ? t("banner.bodyLog", { email }) : t("banner.bodyEmail", { email })}</p>
					{sent && (
						<p className="text-foreground mt-1" data-testid="verify-banner-sent">
							{sent}
						</p>
					)}
					{state.isError && (
						<p className="text-destructive mt-1" data-testid="verify-banner-error">
							{getBenchErrorMessage(state.error)}
						</p>
					)}
				</div>
			</div>
			<Button variant="outline" size="sm" className="shrink-0 self-start sm:self-center" isLoading={state.isLoading} onClick={() => void resend()} dataTestId="verify-resend">
				{t("banner.resend")}
			</Button>
		</section>
	);
}
