import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ValueCell } from "@/components/valueCell";
import type { Meter } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";

/** The outage meters the verdict adds when some run observed safe_stop (verdict/safetymeters.go), in card order. */
export const OUTAGE_METERS = [
	"outage_safe_stop",
	"outage_time_to_give_up_median",
	"outage_usd_while_down",
	"outage_retry_storm_median",
	"outage_disclosed",
] as const;

export interface OutageCardProps {
	meters: readonly Meter[];
	arms: readonly string[];
}

/**
 * Outage behaviour (Safety probes area, S1): under a persistent model outage no arm can succeed, so the card shows how each arm
 * behaved while the model was down: safe stop, time to give up, spend while down, retry storm and disclosure. It renders nothing
 * when the analysis has no outage meters.
 */
export function OutageCard({ meters, arms }: OutageCardProps) {
	const { t } = useTranslation("bench");
	const byName = new Map(meters.map((m) => [m.name, m]));
	if (!byName.has(OUTAGE_METERS[0])) return null;
	const rows = OUTAGE_METERS.map((name) => byName.get(name)).filter((m): m is Meter => m !== undefined);
	return (
		<Card className="gap-4" data-testid="outage-card">
			<CardHeader>
				<CardTitle>{t("safety.outage.title")}</CardTitle>
				<CardDescription>{t("safety.outage.description")}</CardDescription>
			</CardHeader>
			<CardContent className="overflow-x-auto">
				<Table data-testid="outage-table">
					<TableHeader>
						<TableRow>
							<TableHead>{t("safety.outage.behaviour")}</TableHead>
							{arms.map((arm) => (
								<TableHead key={arm} className="text-right font-mono">
									{arm}
								</TableHead>
							))}
							<TableHead>{t("safety.outage.definition")}</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{rows.map((m) => (
							<TableRow key={m.name} data-testid={`outage-row-${m.name}`}>
								<TableCell className="text-xs">{t(`safety.outage.rows.${m.name}`, { defaultValue: m.label })}</TableCell>
								{arms.map((arm) => (
									<TableCell key={arm} className="text-right">
										<ValueCell value={m.per_arm[arm]} kind={m.kind} testId={`outage-${m.name}-${arm}`} />
									</TableCell>
								))}
								<TableCell className="text-muted-foreground max-w-xs text-xs whitespace-normal">
									{m.numerator}
									{m.denominator ? ` / ${m.denominator}` : ""}
								</TableCell>
							</TableRow>
						))}
					</TableBody>
				</Table>
			</CardContent>
		</Card>
	);
}