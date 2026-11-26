import { Button } from "@/components/ui/button";
import { useSession } from "@/lib/session/sessionContext";
import { clearOrgReset, getOrgReset, subscribeOrgReset } from "@/lib/store/apis/session";
import { useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import "./w4b03I18n";

/** The org id of the pending reset notice ("" when none), from the session module's tiny store. */
export function useOrgResetNotice(): string {
	return useSyncExternalStore(subscribeOrgReset, getOrgReset, getOrgReset);
}

/**
 * Shown when this tab asked for an organization the user can no longer open (removed, or the org was
 * deleted): /auth/me answered org_reset and the tab moved to the session's default (wave 4).
 */
export function OrgResetBanner() {
	const { t } = useTranslation("w4b03");
	const session = useSession();
	const org = useOrgResetNotice();
	if (!org) return null;
	const name = session.orgs.find((o) => o.id === org)?.name ?? session.org?.name ?? "";
	return (
		<div
			role="status"
			data-testid="org-reset-banner"
			className="bg-muted/40 flex flex-wrap items-center justify-between gap-3 border-b px-4 py-2 text-sm"
		>
			<span>{t("orgReset.text", { name })}</span>
			<Button size="sm" variant="outline" onClick={clearOrgReset} dataTestId="org-reset-close">
				{t("orgReset.close")}
			</Button>
		</div>
	);
}