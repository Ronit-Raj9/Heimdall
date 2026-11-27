import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { onPlanLimit, type PlanLimitNotice } from "@/lib/billing/planLimit";
import { Link } from "@tanstack/react-router";
import { CreditCard } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

/**
 * The upgrade prompt: when the server refuses an action because the org's plan does not cover it
 * (402 plan_limit, only while billing is on), this dialog says why and links to Settings, Billing.
 * Mounted once in the app shell. It uses the shared Dialog, so focus, Escape and the close X behave
 * like every other dialog.
 */
export function PlanLimitPrompt() {
	const { t } = useTranslation("billing");
	const [notice, setNotice] = useState<PlanLimitNotice | null>(null);
	useEffect(() => onPlanLimit(setNotice), []);
	if (!notice) return null;
	const [path, query] = notice.link.split("?");
	const tab = new URLSearchParams(query ?? "").get("tab") ?? undefined;
	return (
		<Dialog
			open
			onOpenChange={(open) => {
				if (!open) setNotice(null);
			}}
			title={t("limit.title")}
			description={notice.message}
			closeLabel={t("limit.dismiss")}
			size="sm"
			testId="plan-limit-prompt"
			footer={
				<Button size="sm" asChild onClick={() => setNotice(null)}>
					<Link to={path as "/settings"} search={tab ? { tab } : undefined} data-testid="plan-limit-see-plans">
						<CreditCard className="size-4" aria-hidden="true" />
						{t("limit.seePlans")}
					</Link>
				</Button>
			}
		/>
	);
}
