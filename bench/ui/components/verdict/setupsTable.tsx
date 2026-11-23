import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getLocale } from "@/lib/i18n";
import type { Ratio, SetupsVerdict } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import "./hw09I18n";
import { formatPValue } from "./verdict.utils";

/**
 * Every setup of a comparison of three or more (fix wave 3, N244): solved runs, the Holm-adjusted
 * p against the leader, the group (top group or behind) and whether it broke a safety rule. Read
 * from the analysis's setups verdict, so the Result tab and the headline tell one story.
 */
export function SetupsTable({
	setups,
	arms,
	naive,
	names,
}: {
	setups: SetupsVerdict;
	/** Every setup, in the order to show. */
	arms: readonly string[];
	naive: Record<string, Ratio>;
	names?: Record<string, string>;
}) {
	const { t } = useTranslation("hw09");
	const locale = getLocale();
	const pOf = (arm: string): number | null => {
		const pair = setups.pairs.find((p) => (p.arm_x === setups.leader && p.arm_y === arm) || (p.arm_y === setups.leader && p.arm_x === arm));
		return pair?.p_adjusted ?? null;
	};
	const order = [...arms].sort((x, y) => Number(y === setups.leader) - Number(x === setups.leader));
	return (
		<Table data-testid="verdict-setups-table" aria-label={t("setups.title")} containerClassName="rounded-sm border">
			<TableHeader>
				<TableRow className="hover:bg-transparent">
					<TableHead>{t("setups.setup")}</TableHead>
					<TableHead className="text-right">{t("setups.solved")}</TableHead>
					<TableHead className="text-right" title={t("setups.pHint")}>
						{t("setups.pVsLeader")}
					</TableHead>
					<TableHead>{t("setups.group")}</TableHead>
					<TableHead>{t("setups.safety")}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{order.map((arm) => {
					const ratio = naive[arm];
					const p = pOf(arm);
					const broken = setups.safety?.[arm] ?? [];
					const behind = setups.behind.includes(arm);
					const group = arm === setups.leader ? t("setups.leader") : behind ? t("setups.behind") : t("setups.top");
					return (
						<TableRow key={arm} data-testid={`verdict-setups-row-${arm}`} data-group={behind ? "behind" : "top"}>
							<TableCell>
								<span className="font-mono font-semibold">{arm}</span>
								{names?.[arm] && names[arm] !== arm && <span className="text-muted-foreground ml-2 text-xs">{names[arm]}</span>}
							</TableCell>
							<TableCell className="text-right tabular-nums">{ratio ? `${ratio.num} / ${ratio.den}` : "-"}</TableCell>
							<TableCell className="text-right tabular-nums">{p === null ? "-" : formatPValue(p, locale)}</TableCell>
							<TableCell>{group}</TableCell>
							<TableCell className={cn(broken.length > 0 ? "text-chart-error-ink font-medium" : "text-muted-foreground")}>
								{broken.length > 0 ? t("setups.safetyBroken", { tasks: broken.join(", ") }) : t("setups.safetyOk")}
							</TableCell>
						</TableRow>
					);
				})}
			</TableBody>
		</Table>
	);
}