import { Button } from "@/components/ui/button";
import type { SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { EyeOff } from "lucide-react";
import { useTranslation } from "react-i18next";

/** Shown on the held-out board: its scores come from held-out tasks only and never mix with the public board. */
export function LeaderboardHeldOutBanner() {
	const { t } = useTranslation("bench");
	return (
		<div
			role="note"
			data-testid="leaderboard-held-out-banner"
			className="border-chart-info/50 bg-chart-info/10 flex items-start gap-3 rounded-sm border px-4 py-3 text-sm"
		>
			<EyeOff className="text-chart-info-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
			<div className="flex flex-col gap-0.5">
				<p className="text-foreground font-medium">{t("suiteHealth.board.banner")}</p>
				<p className="text-muted-foreground text-xs">{t("suiteHealth.board.bannerHint")}</p>
			</div>
		</div>
	);
}

/** A toggle between the public board and the held-out board (?split=held_out). */
export function HeldOutBoardToggle({
	heldOut,
	onChange,
	disabled,
}: {
	heldOut: boolean;
	onChange: (heldOut: boolean) => void;
	disabled?: boolean;
}) {
	const { t } = useTranslation("bench");
	return (
		<button
			type="button"
			aria-pressed={heldOut}
			disabled={disabled}
			onClick={() => onChange(!heldOut)}
			title={t("suiteHealth.board.toggleHint")}
			data-testid="leaderboard-held-out-toggle"
			className={cn(
				"focus-visible:ring-ring/50 inline-flex w-fit cursor-pointer items-center gap-1.5 rounded-sm border px-2 py-1 text-xs font-medium transition-colors focus-visible:ring-[3px] focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50",
				heldOut ? "border-chart-info/50 bg-chart-info/10 text-foreground" : "text-muted-foreground hover:text-foreground",
			)}
		>
			<EyeOff className="size-3.5 shrink-0" aria-hidden="true" />
			{t("suiteHealth.board.toggle")}
		</button>
	);
}
/**
 * U3: whether to offer the held-out board. The server serves it only with held_out.visible, which
 * the suites API shows: held-out tasks are listed redacted without it. So the toggle shows when some
 * suite has held-out tasks and none of them is redacted.
 */
export function heldOutBoardOffered(
	suites: readonly (Pick<SuiteSummary, "held_out_count"> & { tasks?: SuiteSummary["tasks"]; redacted_tasks?: number })[],
): boolean {
	const withHeldOut = suites.filter((suite) => (suite.held_out_count ?? 0) > 0);
	// M4-34: the light list carries no tasks but counts the redacted ones (G3); the full list is read task by task.
	const noneRedacted = (suite: (typeof suites)[number]) =>
		typeof suite.redacted_tasks === "number" ? suite.redacted_tasks === 0 : (suite.tasks ?? []).every((task) => !task.redacted);
	return withHeldOut.length > 0 && withHeldOut.every(noneRedacted);
}

/** An RTK Query error that is a 404 from the server. */
export function isNotFoundError(error: unknown): boolean {
	return typeof error === "object" && error !== null && (error as { status?: unknown }).status === 404;
}

/** U3: the held-out board was asked for on a server that does not serve it (held_out.visible false). */
export function HeldOutBoardOff({ onBack }: { onBack: () => void }) {
	const { t } = useTranslation("bench");
	return (
		<div
			role="note"
			className="border-chart-info/50 bg-chart-info/10 flex flex-col items-start gap-3 rounded-sm border px-4 py-3 text-sm"
			data-testid="leaderboard-held-out-off"
		>
			<p className="text-foreground flex items-start gap-2">
				<EyeOff className="text-chart-info-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
				{t("suiteHealth.board.off")}
			</p>
			<Button variant="outline" size="sm" onClick={onBack} dataTestId="leaderboard-held-out-back">
				{t("suiteHealth.board.backToPublic")}
			</Button>
		</div>
	);
}