import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getLocale } from "@/lib/i18n";
import type { GateEffect, GateSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { CircleAlert, CircleCheck } from "lucide-react";
import { useTranslation } from "react-i18next";
import { TONE_TEXT, formatCount, type Tone } from "./verdict.utils";

// A fired invalidate gate makes the verdict INVALID; a fired veto zeroes runs; report and
// admission gates inform only (DESIGN.md 9.4, 11).
const FIRED_TONE = {
	invalidate: "bad",
	veto: "warn",
	report: "neutral",
	admission: "neutral",
} as const satisfies Record<GateEffect, Tone>;

export interface GateChecklistProps {
	gates: GateSummary[];
	className?: string;
}

/** Every gate over every run: name, effect, and runs fired over runs checked. */
export function GateChecklist({ gates, className }: GateChecklistProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();

	if (gates.length === 0) {
		return (
			<p className={cn("text-muted-foreground text-sm", className)} data-testid="verdict-gates-empty">
				{t("verdict.gates.empty")}
			</p>
		);
	}

	return (
		<Table className={className} data-testid="verdict-gates-table">
			<TableHeader>
				<TableRow>
					<TableHead>{t("verdict.gates.gate")}</TableHead>
					<TableHead>{t("verdict.gates.effect")}</TableHead>
					<TableHead>{t("verdict.gates.fired")}</TableHead>
					<TableHead>{t("verdict.gates.detail")}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{gates.map((gate) => {
					const fired = gate.fired.num > 0;
					const tone: Tone = fired ? FIRED_TONE[gate.effect] : "good";
					return (
						<TableRow key={gate.gate} data-testid={`verdict-gate-row-${gate.gate}`} data-fired={fired}>
							<TableCell>
								<div className="flex items-center gap-2">
									{fired ? (
										<CircleAlert className={cn("size-4 shrink-0", TONE_TEXT[tone])} aria-label={t("verdict.gates.statusFired")} />
									) : (
										<CircleCheck className="text-chart-success-ink size-4 shrink-0" aria-label={t("verdict.gates.statusClear")} />
									)}
									<span className="font-mono text-xs">{gate.gate}</span>
									<span className="text-muted-foreground font-mono text-[10px]">{gate.version}</span>
									{gate.baseline && (
										<Badge variant="secondary" className="text-[10px]">
											{t("verdict.gates.baseline")}
										</Badge>
									)}
								</div>
							</TableCell>
							<TableCell>
								<Badge variant="outline" className="font-mono text-[10px]">
									{t(`verdict.gates.effectName.${gate.effect}`)}
								</Badge>
							</TableCell>
							<TableCell className="tabular-nums">
								<span className={cn(fired && "font-semibold", fired && TONE_TEXT[tone])}>
									{t("verdict.gates.firedValue", { num: formatCount(gate.fired.num, locale), den: formatCount(gate.fired.den, locale) })}
								</span>
								{gate.fired.unknown > 0 && (
									<span className="bench-hatched text-muted-foreground ml-2 rounded-sm border border-dashed px-1 text-xs">
										{t("verdict.gates.unknown", { count: gate.fired.unknown })}
									</span>
								)}
							</TableCell>
							<TableCell className="text-muted-foreground max-w-md text-xs whitespace-normal">{gate.detail}</TableCell>
						</TableRow>
					);
				})}
			</TableBody>
		</Table>
	);
}