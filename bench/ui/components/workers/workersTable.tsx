import { CopyCodeBlock } from "@/components/suites/copyCodeBlock";
import { ToneBadge } from "@/components/suites/suiteBadges";
import type { Tone } from "@/components/suites/suites.utils";
import { CopyId } from "@/components/ui/copyId";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatCount, formatDateTime } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { WorkerInfo, WorkerState } from "@/lib/types/bench";
import { CircleDashed, CircleDot, CircleX, ServerOff, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";

const STATE_TONE: Record<WorkerState, Tone> = { idle: "neutral", running: "good", lost: "bad" };
const STATE_ICON: Record<WorkerState, LucideIcon> = { idle: CircleDashed, running: CircleDot, lost: CircleX };

function isWorkerState(value: string): value is WorkerState {
	return value === "idle" || value === "running" || value === "lost";
}

/** Idle, running or lost, with what it means. A state this UI does not know shows as sent. */
export function WorkerStateBadge({ state, testId }: { state: string; testId?: string }) {
	const { t } = useTranslation("bench");
	if (!isWorkerState(state)) {
		return (
			<ToneBadge tone="neutral" testId={testId}>
				{state}
			</ToneBadge>
		);
	}
	return (
		<ToneBadge tone={STATE_TONE[state]} icon={STATE_ICON[state]} hint={t(`workers.states.${state}Hint`)} testId={testId}>
			{t(`workers.states.${state}`)}
		</ToneBadge>
	);
}

/** Every registered worker: host, build, state, slots in use, completed runs and when it was last seen. */
export function WorkersTable({ workers }: { workers: readonly WorkerInfo[] }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	return (
		<div className="min-w-0 rounded-sm border">
			<Table stickyColumns={1} aria-label={t("workers.label")} data-testid="workers-table">
				<TableHeader>
					<TableRow>
						<TableHead scope="col">{t("workers.id")}</TableHead>
						<TableHead scope="col">{t("workers.host")}</TableHead>
						<TableHead scope="col">{t("workers.version")}</TableHead>
						<TableHead scope="col">{t("workers.state")}</TableHead>
						<TableHead scope="col" className="text-right">
							{t("workers.running")}
						</TableHead>
						<TableHead scope="col" className="text-right">
							{t("workers.completed")}
						</TableHead>
						<TableHead scope="col">{t("workers.lastSeen")}</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{workers.map((worker) => (
						<TableRow key={worker.id} data-testid={`workers-row-${worker.id}`} data-state={worker.state}>
							<TableCell className="max-w-56 text-xs">
								<CopyId value={worker.id} />
							</TableCell>
							<TableCell className="font-mono text-xs">{worker.host}</TableCell>
							<TableCell className="font-mono text-xs">{worker.version}</TableCell>
							<TableCell>
								<WorkerStateBadge state={worker.state} testId={`workers-row-${worker.id}-state`} />
							</TableCell>
							<TableCell className="text-right text-xs tabular-nums" title={(worker.running ?? []).join(", ") || undefined}>
								{t("workers.runningCell", { count: (worker.running ?? []).length, slots: worker.slots })}
							</TableCell>
							<TableCell className="text-right text-xs tabular-nums">{formatCount(worker.completed, locale)}</TableCell>
							<TableCell className="text-xs">
								<time dateTime={worker.last_seen}>{formatDateTime(worker.last_seen, locale) ?? t("workers.never")}</time>
							</TableCell>
						</TableRow>
					))}
				</TableBody>
			</Table>
		</div>
	);
}

/** The worker API is off: say so plainly, and how to turn it on. Not an error. */
export function WorkersOff() {
	const { t } = useTranslation("bench");
	return (
		<div
			className="bg-card flex min-w-0 flex-col gap-3 rounded-sm border border-dashed px-4 py-4"
			data-testid="workers-off"
			data-state="disabled"
		>
			<p className="text-foreground flex items-center gap-2 text-sm font-medium">
				<ServerOff className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
				{t("workers.offTitle")}
			</p>
			<p className="text-muted-foreground text-xs">{t("workers.offBody")}</p>
			<p className="text-muted-foreground text-xs">{t("workers.offHow")}</p>
			<CopyCodeBlock code="bifrost-bench worker -server <url>" label={t("workers.offCommand")} testId="workers-off-command" />
		</div>
	);
}