import "@/components/settings/ufl8I18n";

import { useConfirm } from "@/components/platform/confirmDialog";
import { InlineError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { NativeSelect } from "@/components/ui/select";
import { ORG_PLANS, useSetOrgPlanMutation, type OrgPlan } from "@/lib/store/apis/adminPlanApi";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** Server admins: set an organization's plan without a payment (PUT /admin/billing/{id}); always asks first. */
export function OrgPlanSetter({ orgId, orgName }: { orgId: string; orgName: string }) {
	const { t } = useTranslation("ufl8");
	const confirm = useConfirm();
	const [plan, setPlan] = useState<OrgPlan>("team");
	const [save, state] = useSetOrgPlanMutation();
	return (
		<div className="bg-card flex min-w-0 flex-wrap items-center gap-2 rounded-xl border p-3 text-sm" data-testid="admin-org-plan">
			<span className="font-medium">{t("plan.title")}</span>
			<NativeSelect value={plan} onChange={(e) => setPlan(e.target.value as OrgPlan)} data-testid="admin-org-plan-select">
				{ORG_PLANS.map((p) => (
					<option key={p} value={p}>
						{t(`plan.name.${p}`)}
					</option>
				))}
			</NativeSelect>
			<Button
				size="sm"
				variant="outline"
				isLoading={state.isLoading}
				disabled={state.isLoading}
				onClick={async () => {
					if (await confirm(t("plan.confirm", { org: orgName, plan: t(`plan.name.${plan}`) }))) void save({ orgId, plan });
				}}
				data-testid="admin-org-plan-save"
			>
				{t("plan.set")}
			</Button>
			{state.data && !state.isLoading && (
				<span className="text-muted-foreground text-xs" data-testid="admin-org-plan-result">
					{t("plan.done", { plan: state.data.effective_plan, status: state.data.status })}
				</span>
			)}
			<InlineError error={state.error} testId="admin-org-plan-error" className="basis-full" />
		</div>
	);
}