import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { Report } from "@/lib/store/apis/evaluationsApi";
import { isAB } from "./reportCard.utils";

/** Lane C09: the report card's Through Bifrost section shows only when some arm has runs. */
export function hasBifrost(report: Report): boolean {
	return (report.bifrost?.length ?? 0) > 0;
}

const count = (v?: number) => (v == null ? "-" : v.toLocaleString("en-US"));

/** "Through Bifrost": per agent, fallbacks, cache hits, retries, input tokens and cost Bifrost recorded. */
export function BifrostTable({ report }: { report: Report }) {
	const ab = isAB(report);
	const rows = report.bifrost ?? [];
	return (
		<Table data-testid="report-bifrost" aria-label="Through Bifrost">
			<TableHeader>
				<TableRow>
					<TableHead scope="col">Agent</TableHead>
					<TableHead scope="col" className="text-right">Runs (all, normal and under fault)</TableHead>
					<TableHead scope="col" className="text-right">Runs that used a fallback</TableHead>
					<TableHead scope="col" className="text-right">Cache hits</TableHead>
					<TableHead scope="col" className="text-right">Retries</TableHead>
					<TableHead scope="col" className="text-right">Input tokens</TableHead>
					<TableHead scope="col" className="text-right">Cost (all runs)</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((row) => (
					<TableRow key={row.arm} data-testid={`bifrost-${row.arm}`}>
						<TableHead scope="row" className="font-medium">
							{ab ? row.arm : "Your agent"}
						</TableHead>
						<TableCell className="text-right tabular-nums">{row.runs}</TableCell>
						<TableCell className="text-right tabular-nums">
							{row.fallback_runs == null ? "-" : `${row.fallback_runs} of ${row.fallback_known}`}
						</TableCell>
						<TableCell className="text-right tabular-nums">{count(row.cache_hits)}</TableCell>
						<TableCell className="text-right tabular-nums">{count(row.retries)}</TableCell>
						<TableCell className="text-right tabular-nums">{count(row.input_tokens)}</TableCell>
						<TableCell className="text-right tabular-nums">
							{row.cost_usd == null ? "-" : `$${row.cost_usd.toFixed(4)}`}
							{row.cost_usd != null && row.cost_runs < row.runs && (
								<span className="text-muted-foreground block text-xs">{`${row.cost_runs} of ${row.runs} runs`}</span>
							)}
						</TableCell>
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}
