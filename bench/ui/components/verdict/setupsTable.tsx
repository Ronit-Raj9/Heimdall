import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getLocale } from "@/lib/i18n";
import type { Ratio, SetupsVerdict } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import "./hw09I18n";
import "@/components/experiments/ufrcI18n";
import { formatUsd2 } from "./armCost";
import { formatPValue } from "./verdict.utils";

type Translate = (key: string, opts?: Record<string, unknown>) => string;

/** The most tasks any pair of setups shares: the tasks the setups were compared on. Pure. */
export function sharedTasks(setups: Pick<SetupsVerdict, "pairs">): number {
	return setups.pairs.reduce((most, pair) => Math.max(most, pair.tasks), 0);
}

/**
 * The server's setups note in plain words (Q-140, M4-46): its text reads like a statistics paper
 * (Holm, alpha, sign-flip), so the page says what it means; the raw note stays for statisticians.
 * t is the ufrc namespace. Pure.
 */
export function plainSetupsNote(setups: Pick<SetupsVerdict, "pairs" | "separable" | "leader">, t: Translate): string {
	const count = sharedTasks(setups);
	return setups.separable ? t("setups.noteApart", { count, leader: setups.leader }) : t("setups.noteTooFew", { count });
}

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
	costs,
}: {
	setups: SetupsVerdict;
	/** Every setup, in the order to show. */
	arms: readonly string[];
	naive: Record<string, Ratio>;
	names?: Record<string, string>;
	/** Q-141: the median cost per run of each setup (from the diagnostics), shown on the phone cards. */
	costs?: Record<string, number | null>;
}) {
	const { t } = useTranslation("hw09");
	const { t: tr } = useTranslation("ufrc");
	const locale = getLocale();
	const pOf = (arm: string): number | null => {
		const pair = setups.pairs.find((p) => (p.arm_x === setups.leader && p.arm_y === arm) || (p.arm_y === setups.leader && p.arm_x === arm));
		return pair?.p_adjusted ?? null;
	};
	const order = [...arms].sort((x, y) => Number(y === setups.leader) - Number(x === setups.leader));
	// M4-48: when every setup is tied with the leader the group says nothing: it is left out.
	const allTied = setups.behind.length === 0;
	const groupOf = (arm: string) =>
		arm === setups.leader ? t("setups.leader") : setups.behind.includes(arm) ? t("setups.behind") : t("setups.top");
	return (
		<>
			{/* M4-47: one card per setup on a phone, so the solved count and the group stay in view. */}
			<ul className="flex flex-col gap-2 sm:hidden" data-testid="verdict-setups-cards" aria-label={t("setups.title")}>
				{order.map((arm) => {
					const ratio = naive[arm];
					const broken = setups.safety?.[arm] ?? [];
					return (
						<li key={arm} className="flex flex-col gap-1 rounded-sm border p-3 text-sm" data-testid={`verdict-setups-card-${arm}`}>
							<div className="flex items-baseline justify-between gap-2">
								<span className="min-w-0 break-words">
									<span className="font-semibold">{arm}</span>
									{names?.[arm] && names[arm] !== arm && <span className="text-muted-foreground ml-2 text-xs">{names[arm]}</span>}
								</span>
								<span className="shrink-0 tabular-nums">{ratio ? `${ratio.num} / ${ratio.den}` : "-"}</span>
							</div>
							{costs?.[arm] != null && (
								<span className="text-muted-foreground text-xs tabular-nums" data-testid={`verdict-setups-card-${arm}-cost`}>
									{tr("setups.cardCost", { cost: formatUsd2(costs[arm] as number, locale) })}
								</span>
							)}
							{!allTied && <span className="text-muted-foreground text-xs">{groupOf(arm)}</span>}
							{broken.length > 0 && (
								<span className="text-chart-error-ink text-xs font-medium">{t("setups.safetyBroken", { tasks: broken.join(", ") })}</span>
							)}
						</li>
					);
				})}
			</ul>
			<Table data-testid="verdict-setups-table" aria-label={t("setups.title")} containerClassName="hidden rounded-sm border sm:block">
				<TableHeader>
					<TableRow className="hover:bg-transparent">
						<TableHead>{t("setups.setup")}</TableHead>
						<TableHead className="text-right">{t("setups.solved")}</TableHead>
						<TableHead className="text-right" title={t("setups.pHint")}>
							{t("setups.pVsLeader")}
						</TableHead>
						{!allTied && <TableHead>{t("setups.group")}</TableHead>}
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
								{!allTied && <TableCell>{group}</TableCell>}
								<TableCell className={cn(broken.length > 0 ? "text-chart-error-ink font-medium" : "text-muted-foreground")}>
									{broken.length > 0 ? t("setups.safetyBroken", { tasks: broken.join(", ") }) : t("setups.safetyOk")}
								</TableCell>
							</TableRow>
						);
					})}
				</TableBody>
			</Table>
		</>
	);
}