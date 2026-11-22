import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { Report, ReportRedTeamArm, ReportRedTeamRow } from "@/lib/store/apis/evaluationsApi";
import { isAB, pct } from "./reportCard.utils";

// Z04 red team: attack success by category, strategy and channel, attempts to first break and the
// hardest attack that got through. Lower is better everywhere in this section.

export function hasRedTeam(report: Report): boolean {
	return Object.keys(report.red_team?.arms ?? {}).length > 0;
}

function rateText(r: { success_rate: number; ci_low: number; ci_high: number; broken: number; attacks: number }): string {
	return `${pct(r.success_rate)} (${r.broken} of ${r.attacks}, 95% range ${pct(r.ci_low)}-${pct(r.ci_high)})`;
}

function attemptsText(n: number | null | undefined): string {
	if (n == null) return "Never broken";
	const v = Number.isInteger(n) ? n : Number(n.toFixed(1));
	return `${v} ${v === 1 ? "attempt" : "attempts"}`;
}

function GroupTable({ title, rows, testId }: { title: string; rows: ReportRedTeamRow[]; testId: string }) {
	if (rows.length === 0) return null;
	return (
		<Table data-testid={testId} aria-label={title}>
			<TableHeader>
				<TableRow>
					<TableHead scope="col">{title}</TableHead>
					<TableHead scope="col" className="text-right">
						Attacks that got through
					</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((r) => (
					<TableRow key={r.id} data-testid={`${testId}-${r.id}`}>
						<TableHead scope="row" className="font-medium">
							{r.label}
						</TableHead>
						<TableCell className="text-right tabular-nums">{rateText(r)}</TableCell>
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}

function ArmBlock({ name, arm }: { name: string; arm: ReportRedTeamArm }) {
	return (
		<div className="flex flex-col gap-4" data-testid={`red-team-arm-${name}`}>
			<dl className="grid gap-3 sm:grid-cols-3">
				<div className="bg-card rounded-xl border p-3">
					<dt className="text-muted-foreground text-xs">Attack success rate</dt>
					<dd className="text-foreground text-xl font-semibold tabular-nums" data-testid="red-team-rate">
						{pct(arm.success_rate)}
					</dd>
					<dd className="text-muted-foreground text-xs tabular-nums">
						{arm.broken} of {arm.attacks} attacks, 95% range {pct(arm.ci_low)}-{pct(arm.ci_high)}
					</dd>
				</div>
				<div className="bg-card rounded-xl border p-3">
					<dt className="text-muted-foreground text-xs">Attempts to first break</dt>
					<dd className="text-foreground text-xl font-semibold tabular-nums" data-testid="red-team-attempts">
						{attemptsText(arm.median_attempts_to_break)}
					</dd>
					<dd className="text-muted-foreground text-xs">
						{arm.fewest_attempts_to_break == null ? "No attack got through" : `median; quickest break took ${attemptsText(arm.fewest_attempts_to_break)}`}
					</dd>
				</div>
				<div className="bg-card rounded-xl border p-3">
					<dt className="text-muted-foreground text-xs">Hardest attack that got through</dt>
					<dd className="text-foreground text-sm font-semibold" data-testid="red-team-hardest">
						{arm.hardest ? arm.hardest.summary : "None"}
					</dd>
					<dd className="text-muted-foreground text-xs">
						{arm.hardest ? `after ${attemptsText(arm.hardest.attempts_to_break)}, run ${arm.hardest.run}` : "The agent held against every attack"}
					</dd>
				</div>
			</dl>
			{arm.most_effective && (
				<p className="text-muted-foreground text-sm" data-testid="red-team-most-effective">
					Most effective: <span className="text-foreground font-medium">{arm.most_effective.label}</span>, {rateText(arm.most_effective)}.
				</p>
			)}
			<div className="grid gap-4 lg:grid-cols-3">
				<GroupTable title="Category" rows={arm.by_category} testId={`red-team-${name}-category`} />
				<GroupTable title="Strategy" rows={arm.by_strategy} testId={`red-team-${name}-strategy`} />
				<GroupTable title="Channel" rows={arm.by_channel} testId={`red-team-${name}-channel`} />
			</div>
			{arm.regressions > 0 && (
				<p className="text-muted-foreground text-xs">{arm.regressions} of these runs replayed earlier successful attacks (regressions).</p>
			)}
		</div>
	);
}

/** "Red team": adaptive attacks on the agent. */
export function RedTeamSection({ report }: { report: Report }) {
	const ab = isAB(report);
	const arms = report.red_team?.arms ?? {};
	return (
		<div className="flex flex-col gap-6" data-testid="report-red-team">
			{Object.entries(arms).map(([label, arm]) => (
				<div key={label} className="flex flex-col gap-2">
					{ab && <h3 className="text-sm font-semibold">Agent {label}</h3>}
					<ArmBlock name={label} arm={arm} />
				</div>
			))}
		</div>
	);
}
