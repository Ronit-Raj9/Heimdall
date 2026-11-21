import { JobRunButton } from "@/components/jobs/jobRunButton";
import { Button } from "@/components/ui/button";
import type { InboxDraft } from "@/lib/types/bench";
import { SUITE_OPS_KINDS } from "@/lib/types/suiteops";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

/** The suite and task of a draft ("suite/task"); null when the draft names neither. */
export function draftTarget(draft: Pick<InboxDraft, "task">): { suite: string; task: string } | null {
	const [suite, task] = draft.task.split("/");
	return suite && task ? { suite, task } : null;
}

/**
 * After a failure became a draft task (X13): open the task's files to finish it, then run its
 * readiness check from here, with live progress and the result.
 */
export function InboxDraftNext({ draft }: { draft: InboxDraft }) {
	const { t } = useTranslation("suiteBuild");
	const target = draftTarget(draft);
	if (!target) return null;
	return (
		<div className="flex flex-col gap-2" data-testid="inbox-draft-next-actions">
			<div className="flex flex-wrap items-start gap-2">
				<Button asChild size="sm" variant="outline">
					<Link to="/suites/$suiteId" params={{ suiteId: target.suite }} search={{ tab: "edit" } as never} data-testid="inbox-draft-open">
						{t("inbox.open")}
					</Link>
				</Button>
				<JobRunButton
					kind={SUITE_OPS_KINDS.admit}
					params={{ suite: target.suite, task: target.task }}
					label={t("inbox.check")}
					confirm={t("readiness.confirmOne", { task: draft.task })}
					variant="outline"
				/>
			</div>
			<p className="text-muted-foreground text-xs">{t("inbox.hint")}</p>
		</div>
	);
}
