import { EntityLink } from "@/components/shell/entityLink";
import { formatDuration } from "@/components/runs/traceView.utils";
import type { RunDebug } from "@/lib/types/runDebug";
import { Bug } from "lucide-react";
import { useTranslation } from "react-i18next";
import { conditionName } from "./debugActions";

/** The note at the top of a debug run: what it came from, that it never counts, and time held. */
export function DebugBanner({ debug }: { debug: RunDebug }) {
	const { t } = useTranslation("runDebug");
	const run = debug.from_run ?? "";
	return (
		<div className="bg-muted/40 flex flex-col gap-1 rounded-xl border px-4 py-3 text-sm" data-testid="debug-banner" data-mode={debug.mode}>
			<span className="flex flex-wrap items-center gap-2 font-medium">
				<Bug className="text-muted-foreground size-4" aria-hidden="true" />
				{t(`banner.${debug.mode}`, { run, call: debug.call ?? 0 })}
				{run && <EntityLink kind="run" id={run} />}
			</span>
			{debug.from_condition && (
				<span className="text-muted-foreground text-xs" data-testid="debug-banner-condition">
					{t("banner.fromCondition", { fault: conditionName(debug.from_condition) })}
				</span>
			)}
			<span className="text-muted-foreground text-xs">{t("banner.note")}</span>
			{debug.held_ms ? (
				<span className="text-muted-foreground text-xs">{t("banner.held", { time: formatDuration(debug.held_ms) })}</span>
			) : null}
		</div>
	);
}