import { Button } from "@/components/ui/button";
import { onPlanLimit, type PlanLimitNotice } from "@/lib/billing/planLimit";
import { Link } from "@tanstack/react-router";
import { CreditCard, X } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

/**
 * The upgrade prompt: when the server refuses an action because the org's plan does not cover it
 * (402 plan_limit, only while billing is on), this card says why and links to Settings, Billing.
 * Mounted once in the app shell.
 */
export function PlanLimitPrompt() {
	const { t } = useTranslation("billing");
	const [notice, setNotice] = useState<PlanLimitNotice | null>(null);
	useEffect(() => onPlanLimit(setNotice), []);
	if (!notice) return null;
	const [path, query] = notice.link.split("?");
	const tab = new URLSearchParams(query ?? "").get("tab") ?? undefined;
	return (
		<div
			role="alertdialog"
			aria-labelledby="plan-limit-title"
			aria-describedby="plan-limit-body"
			className="bg-card animate-in fade-in slide-in-from-bottom-2 fixed right-4 bottom-4 z-50 flex w-[min(24rem,calc(100vw-2rem))] flex-col gap-3 rounded-xl border p-4 shadow-lg"
			data-testid="plan-limit-prompt"
		>
			<div className="flex items-start justify-between gap-3">
				<h2 id="plan-limit-title" className="text-foreground text-sm font-semibold">
					{t("limit.title")}
				</h2>
				<button
					type="button"
					onClick={() => setNotice(null)}
					className="text-muted-foreground hover:text-foreground -m-1 rounded p-1"
					aria-label={t("limit.dismiss")}
					data-testid="plan-limit-dismiss"
				>
					<X className="size-4" aria-hidden="true" />
				</button>
			</div>
			<p id="plan-limit-body" className="text-muted-foreground text-[13px] leading-relaxed">
				{notice.message}
			</p>
			<div>
				<Button size="sm" asChild onClick={() => setNotice(null)}>
					<Link to={path as "/settings"} search={tab ? { tab } : undefined} data-testid="plan-limit-see-plans">
						<CreditCard className="size-4" aria-hidden="true" />
						{t("limit.seePlans")}
					</Link>
				</Button>
			</div>
		</div>
	);
}
