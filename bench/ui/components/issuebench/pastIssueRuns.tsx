import { BlockSkeleton, InlineError, LoadError, Section } from "@/components/runs/screenStates";
import "@/components/suites/ufl5I18n";
import { Button } from "@/components/ui/button";
import { useCancelIssueRunMutation, useListIssueRunsQuery } from "@/lib/store/apis/ufl5Api";
import type { IssueRun } from "@/lib/types/issuebench";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** Past scans, newest first. */
export function newestRuns(runs: readonly IssueRun[] | null | undefined): IssueRun[] {
	return [...(runs ?? [])].sort((a, b) => b.created.localeCompare(a.created));
}

/** Only a running build can be stopped. */
export function canCancel(r: Pick<IssueRun, "status">): boolean {
	return r.status === "running";
}

/** Past issue scans (GET /issuebench/runs): open one, stop a running one (POST .../cancel). Hidden while there are none. */
export function PastIssueRuns({ openId, onOpen, writable }: { openId: string; onOpen(id: string): void; writable: boolean }) {
	const { t } = useTranslation("ufl5");
	const q = useListIssueRunsQuery();
	const [cancel, st] = useCancelIssueRunMutation();
	const [error, setError] = useState<unknown>(null);
	const runs = newestRuns(q.data?.runs);
	if (!q.isLoading && !q.error && runs.length === 0) return null;
	return (
		<Section title={t("issues.title")} as="h3" testId="issuebench-past">
			{q.isLoading ? (
				<BlockSkeleton rows={2} testId="issuebench-past-loading" />
			) : q.error ? (
				<LoadError title={t("issues.loadError")} error={q.error} onRetry={() => void q.refetch()} testId="issuebench-past-error" />
			) : (
				<ul className="bg-card flex min-w-0 flex-col rounded-xl border">
					{runs.map((r) => (
						<li key={r.id} className="flex flex-wrap items-center gap-3 border-b px-4 py-2 last:border-0" data-testid={`issuebench-past-${r.id}`}>
							<span className="min-w-0 flex-1 truncate font-mono text-xs">{r.repo}</span>
							<span className="text-muted-foreground text-xs">{t(`issues.status.${r.status}` as "issues.status.done", { defaultValue: r.status })}</span>
							<span className="text-muted-foreground text-xs tabular-nums">{new Date(r.created).toLocaleString()}</span>
							{r.id === openId ? (
								<span className="text-xs font-medium">{t("issues.shown")}</span>
							) : (
								<Button size="sm" variant="ghost" onClick={() => onOpen(r.id)} data-testid={`issuebench-past-open-${r.id}`}>
									{t("issues.open")}
								</Button>
							)}
							{writable && canCancel(r) && (
								<Button
									size="sm"
									variant="outline"
									disabled={st.isLoading}
									onClick={() => {
										setError(null);
										cancel(r.id)
											.unwrap()
											.catch((e: unknown) => setError(e));
									}}
									data-testid={`issuebench-past-cancel-${r.id}`}
								>
									{t("issues.cancel")}
								</Button>
							)}
						</li>
					))}
				</ul>
			)}
			<InlineError error={error} testId="issuebench-past-cancel-error" />
		</Section>
	);
}
