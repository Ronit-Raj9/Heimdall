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