import { benchApi } from "@/lib/store/apis/benchApi";
import type { ReplayLabel } from "@/lib/types/runDebug";
import { cn } from "@/lib/utils";
import { useContext } from "react";
import { useTranslation } from "react-i18next";
import { ReactReduxContext } from "react-redux";
import { pageRunId } from "./debugBanner";
import { hw11 } from "./hw11I18n";

const TONE: Record<ReplayLabel, string> = {
	hit: "bg-muted text-muted-foreground",
	edited: "bg-primary/10 text-primary",
	divergence: "bg-chart-error/15 text-chart-error-ink",
	exhausted: "bg-chart-warning/15 text-chart-warning-ink",
};

/** What a fork changed at its fork call (run.json debug.edit, fix wave 3 N247). */
export type ForkEdit = "message" | "response";

/** The chip text of an edited call that says what changed, or null when that is not known. */
export function editedText(label: ReplayLabel, edit: ForkEdit | undefined): string | null {
	if (label === "edited" && edit === "message") return hw11("replay.editedMessage");
	if (label === "edited" && edit === "response") return hw11("replay.editedResponse");
	return null;
}

/** How a replayed model call was answered: from the recording, edited, or different from it. */
export function ReplayBadge({
	label,
	testId,
	edit,
	className,
}: {
	label: ReplayLabel;
	testId?: string;
	edit?: ForkEdit;
	className?: string;
}) {
	const { t } = useTranslation("runDebug");
	return (
		<span
			className={cn("rounded-full px-2 py-0.5 text-[11px] font-medium", TONE[label], className)}
			data-testid={testId}
			data-replay={label}
		>
			{editedText(label, edit) ?? t(`replay.${label}`)}
		</span>
	);
}

/**
 * The trace row chip of a fork's edited call: "Message added here" or "Answer replaced here", from the
 * run page's run (already in the store, read without a new request); "Edited" when it is not known.
 */
export function EditedCallBadge({ call, testId }: { call: number; testId?: string }) {
	const redux = useContext(ReactReduxContext);
	const runId = pageRunId();
	let edit: ForkEdit | undefined;
	if (redux && runId) {
		const debug = benchApi.endpoints.getRun.select(runId)(redux.store.getState() as never)?.data?.run?.debug;
		if (debug?.call === call) edit = debug.edit;
	}
	return <ReplayBadge label="edited" edit={edit} testId={testId} />;
}