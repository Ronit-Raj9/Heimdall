import { useGetRunDiffQuery } from "@/lib/store/apis/benchApi";
import { errorStatus } from "@/lib/store/apis/session";
import type { OutputKind, RunDetail } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";
import { DiffText } from "./diffText";
import { ArtifactFiles } from "./runArtifacts";
import { AnswerBlock, StateFiles } from "./runScenario";
import { BlockSkeleton, EmptyNote, LoadError } from "./screenStates";

const OUTPUT_KINDS: readonly OutputKind[] = ["diff", "answer", "state", "artifact"];

/**
 * What a run produced, which decides how its Output is shown (UI regroup, U2): the kind the suite
 * names when it is known (the run list carries it), else what the run recorded: an answer, artifact
 * files or exported state; a run with none of them is a diff run. Pure.
 */
export function runOutputKind(detail: Pick<RunDetail, "answer" | "artifacts" | "state"> | undefined, hint?: string | null): OutputKind {
	if (hint && (OUTPUT_KINDS as readonly string[]).includes(hint)) return hint as OutputKind;
	if (detail?.answer) return "answer";
	if (detail?.artifacts?.length) return "artifact";
	if (detail?.state?.length) return "state";
	return "diff";
}

/**
 * A run's output in the form its kind calls for: the patch for a diff run, the answer, the exported
 * state files, or the artifact files. Only a diff run asks for the diff, so an answer, memory or
 * tool run never shows "Could not load the diff".
 */
export function RunOutput({ detail, arm, kind }: { detail: RunDetail; arm: string; kind?: OutputKind }) {
	const { t } = useTranslation("bench");
	const output = kind ?? runOutputKind(detail);
	const testId = `run-output-${arm}`;
	switch (output) {
		case "answer":
			return (
				<div data-testid={testId} data-output="answer">
					{detail.answer ? (
						<AnswerBlock answer={detail.answer} />
					) : (
						<EmptyNote testId={`${testId}-none`}>{t("runs.output.noAnswer")}</EmptyNote>
					)}
				</div>
			);
		case "state":
			return (
				<div data-testid={testId} data-output="state">
					{detail.state?.length ? (
						<StateFiles files={detail.state} arm={arm} />
					) : (
						<EmptyNote testId={`${testId}-none`}>{t("runs.output.noState")}</EmptyNote>
					)}
				</div>
			);
		case "artifact":
			return (
				<div data-testid={testId} data-output="artifact">
					{detail.artifacts?.length ? (
						<ArtifactFiles
							files={detail.artifacts}
							runId={detail.run.run_id}
							arm={arm}
							sealed={detail.sealed}
							sealError={detail.seal_error}
						/>
					) : (
						<EmptyNote testId={`${testId}-none`}>{t("runs.output.noArtifacts")}</EmptyNote>
					)}
				</div>
			);
		case "diff":
			return (
				<div data-testid={testId} data-output="diff">
					<RunDiff runId={detail.run.run_id} arm={arm} sealed={detail.sealed} />
				</div>
			);
	}
}

/** The patch of a diff run. A 404 is no patch yet (in flight) or none at all, said plainly; any other failure is an error. */
function RunDiff({ runId, arm, sealed }: { runId: string; arm: string; sealed: boolean }) {
	const { t } = useTranslation("bench");
	const diff = useGetRunDiffQuery(runId);
	if (diff.isLoading) return <BlockSkeleton rows={6} testId={`diff-${arm}-loading`} />;
	if (errorStatus(diff.error) === 404) {
		return <EmptyNote testId={`diff-${arm}-none`}>{sealed ? t("runs.output.noDiff") : t("runs.output.noDiffYet")}</EmptyNote>;
	}
	if (diff.error || !diff.data) {
		return (
			<LoadError title={t("runs.diff.loadError")} error={diff.error} onRetry={() => void diff.refetch()} testId={`diff-${arm}-error`} />
		);
	}
	return <DiffText runDiff={diff.data} arm={arm} />;
}