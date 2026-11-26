import { Section } from "@/components/runs/screenStates";
import { useGetBifrostSettingQuery, useGetGatewayEffectQuery } from "@/lib/store/apis/gatewayApi";
import type { GwArmResult, GwBifrostSettingAnswer, GwEffect, GwResult } from "@/lib/types/gateway";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { UseInBifrostButton } from "./useInBifrost";

/** Text color of a result word: Saved green, Lost and Failed red, Worked gray. */
export const RESULT_TONE: Record<GwResult, string> = {
	saved: "text-emerald-700 dark:text-emerald-400",
	lost: "text-red-700 dark:text-red-400",
	worked: "text-muted-foreground",
	failed: "text-red-700 dark:text-red-400",
};

const pct = (v?: number) => (v === undefined ? "" : `${Math.round(v * 100)}%`);
const usd = (v?: number) => (v === undefined ? "" : `$${v.toFixed(4)}`);
const ms = (v?: number) => (v === undefined ? "" : `${Math.round(v)} ms`);

/** The value of one details cell, as plain text. */
export function detailText(v: unknown): string {
	if (v === null || v === undefined) return "";
	if (typeof v === "string" || typeof v === "number" || typeof v === "boolean") return String(v);
	return JSON.stringify(v);
}

function ArmsTable({ arms, control }: { arms: GwArmResult[]; control: string }) {
	const { t } = useTranslation("w4p6");
	return (
		<div className="overflow-x-auto">
			<table className="w-full text-sm" data-testid="gateway-effect-arms">
				<thead className="text-muted-foreground text-left text-xs">
					<tr>
						<th className="py-1 pr-3 font-normal">{t("effect.setup")}</th>
						<th className="py-1 pr-3 font-normal">{t("effect.runs")}</th>
						<th className="py-1 pr-3 font-normal">{t("effect.tasks")}</th>
						<th className="py-1 pr-3 font-normal">{t("effect.solveRate")}</th>
						<th className="py-1 pr-3 font-normal">{t("effect.costPerTask")}</th>
						<th className="py-1 font-normal">{t("effect.latency")}</th>
					</tr>
				</thead>
				<tbody>
					{arms.map((a) => (
						<tr key={a.arm} className="border-t" data-testid={`gateway-effect-arm-${a.arm}`}>
							<td className="py-1 pr-3">
								{a.label}
								<span className="text-muted-foreground ml-2 text-xs">{a.arm === control ? t("effect.control") : t("effect.variant")}</span>
							</td>
							<td className="py-1 pr-3">{a.runs}</td>
							<td className="py-1 pr-3">{a.tasks}</td>
							<td className="py-1 pr-3">{pct(a.solve_rate)}</td>
							<td className="py-1 pr-3">{usd(a.cost_per_run)}</td>
							<td className="py-1">{ms(a.latency_ms)}</td>
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}

/** The Effect of one feature test: state, headline, setups, the feature's details and the button. Pure. */
export function GatewayEffectView({ effect, setting }: { effect: GwEffect; setting?: GwBifrostSettingAnswer }) {
	const { t } = useTranslation("w4p6");
	const details = Object.entries(effect.details ?? {});
	return (
		<Section title={t("effect.title")} testId="gateway-effect-card">
			<div className="flex flex-col gap-3" data-state={effect.state}>
				<div className="flex flex-wrap items-center gap-2 text-sm">
					<span className="bg-muted rounded-full px-2 py-0.5 text-xs" data-testid="gateway-effect-state">
						{t(`effect.state.${effect.state}`)}
					</span>
					{effect.result && <span className={cn("font-medium", RESULT_TONE[effect.result])}>{t(`line.result.${effect.result}`)}</span>}
					<span data-testid="gateway-effect-headline">{effect.headline}</span>
				</div>
				{effect.reason && effect.state !== "measured" && (
					<p className="text-muted-foreground text-sm" data-testid="gateway-effect-reason">
						{t("line.unknown", { reason: effect.reason })}
					</p>
				)}
				<p className="text-muted-foreground text-xs">{t("line.fromNoDate", { source: effect.source.text })}</p>
				{effect.arms.length > 0 && <ArmsTable arms={effect.arms} control={effect.control} />}
				{details.length > 0 && (
					<div className="flex flex-col gap-1">
						<h4 className="text-sm font-medium">{t("effect.details")}</h4>
						<table className="text-sm" data-testid="gateway-effect-details">
							<tbody>
								{details.map(([k, v]) => (
									<tr key={k} className="border-t">
										<td className="text-muted-foreground py-1 pr-3">{k}</td>
										<td className="py-1">{detailText(v)}</td>
									</tr>
								))}
							</tbody>
						</table>
					</div>
				)}
				<div>
					<UseInBifrostButton setting={setting ? setting.setting : effect.setting} reason={setting?.reason} />
				</div>
			</div>
		</Section>
	);
}

/** Experiment page: the feature test's Effect. Renders nothing when the comparison is not a feature test (404) or on any error. */
export function GatewayEffectCard({ expId }: { expId: string }) {
	const effect = useGetGatewayEffectQuery(expId);
	const setting = useGetBifrostSettingQuery(expId, { skip: !effect.data });
	if (!effect.data) return null;
	return <GatewayEffectView effect={effect.data} setting={setting.data} />;
}