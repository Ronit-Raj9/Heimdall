import type { InboxDraft } from "@/lib/types/bench";
import { AlertTriangle, PencilLine } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * What an accept wrote: the draft task, the files still marked EDIT ME and the next steps. The
 * draft is not a finished test, and this says so first.
 */
export function InboxDraftResult({ draft }: { draft: InboxDraft }) {
	const { t } = useTranslation("bench");
	// Go writes an empty list as null; read it as empty.
	const warnings = draft.warnings ?? [];
	const editMe = draft.edit_me ?? [];
	const next = draft.next ?? [];
	return (
		<div
			className="border-primary/40 bg-primary/5 flex flex-col gap-3 rounded-lg border px-4 py-3 text-sm"
			role="status"
			data-testid="inbox-draft"
		>
			<p className="text-foreground font-medium">{t("inbox.draft.title", { task: draft.task })}</p>
			<p className="flex items-start gap-2 font-medium" data-testid="inbox-draft-needs-human">
				<PencilLine className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
				{t("inbox.draft.needsHuman")}
			</p>
			<dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">
				<dt className="text-muted-foreground">{t("inbox.draft.dir")}</dt>
				<dd className="font-mono break-all">{draft.dir}</dd>
				<dt className="text-muted-foreground">{t("inbox.draft.template")}</dt>
				<dd className="font-mono">{draft.template}</dd>
			</dl>
			{editMe.length > 0 && (
				<div className="flex flex-col gap-1">
					<p className="text-muted-foreground text-xs">{t("inbox.draft.editMe")}</p>
					<ul className="flex flex-col gap-0.5 font-mono text-xs" data-testid="inbox-draft-edit-me">
						{editMe.map((file) => (
							<li key={file} className="break-all">
								{file}
							</li>
						))}
					</ul>
				</div>
			)}
			{next.length > 0 && (
				<div className="flex flex-col gap-1">
					<p className="text-muted-foreground text-xs">{t("inbox.draft.next")}</p>
					<ol className="flex list-decimal flex-col gap-1 pl-5 text-xs" data-testid="inbox-draft-next">
						{next.map((step, i) => (
							<li key={`${i}-${step}`}>
								<code className="bg-card rounded-sm border px-1.5 py-0.5 font-mono break-all">{step}</code>
							</li>
						))}
					</ol>
				</div>
			)}
			{warnings.length > 0 && (
				<div className="flex flex-col gap-1" data-testid="inbox-draft-warnings">
					<p className="flex items-center gap-1.5 text-xs font-medium">
						<AlertTriangle className="text-chart-warning-ink size-3.5" aria-hidden="true" />
						{t("inbox.draft.warnings")}
					</p>
					<ul className="text-muted-foreground list-disc pl-5 text-xs break-words">
						{warnings.map((warning, i) => (
							<li key={`${i}-${warning}`}>{warning}</li>
						))}
					</ul>
				</div>
			)}
		</div>
	);
}