import { InboxActions, type InboxAction } from "@/components/inbox/inboxActions";
import { InboxItemDetailView } from "@/components/inbox/inboxDetail";
import { InboxDraftResult } from "@/components/inbox/inboxDraft";
import { InboxDraftNext } from "@/components/inbox/inboxDraftNext";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	useAcceptInboxItemMutation,
	useGetInboxItemQuery,
	useRejectInboxItemMutation,
	useSnoozeInboxItemMutation,
} from "@/lib/store/apis/inboxApi";
import type { InboxSuite } from "@/lib/types/bench";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import "./uf7aI18n";

/** The kinds the server's redaction removes before an item is stored (bench traceimport redact.go), in the order shown. */
export const REMOVED_KINDS = ["secret", "email", "phone", "number", "path"] as const;

/** Counts of removed values grouped into the shown kinds; the server's own kind names fold into these. Exported for tests. */
export function removedByKind(redacted: Record<string, number> | undefined): Record<(typeof REMOVED_KINDS)[number], number> {
	const out = { secret: 0, email: 0, phone: 0, number: 0, path: 0 };
	for (const [kind, n] of Object.entries(redacted ?? {})) {
		const k = kind.toLowerCase();
		if (k.includes("email")) out.email += n;
		else if (k.includes("phone")) out.phone += n;
		else if (k.includes("number") || k.includes("card")) out.number += n;
		else if (k.includes("path") || k.includes("home")) out.path += n;
		else out.secret += n;
	}
	return out;
}

/** "What is kept": what the item stores and what was removed before it was stored, with counts (M7-3). */
export function WhatIsKept({ redacted }: { redacted?: Record<string, number> }) {
	const { t } = useTranslation("uf7a");
	const removed = removedByKind(redacted);
	return (
		<div className="bg-card rounded-md border p-3 text-sm" data-testid="inbox-what-is-kept">
			<p className="font-medium">{t("kept.title")}</p>
			<p className="text-muted-foreground mt-1">{t("kept.sentence")}</p>
			<dl className="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-2">
				<div>
					<dt className="font-medium">{t("kept.kept")}</dt>
					<dd className="text-muted-foreground">{t("kept.keptList")}</dd>
				</div>
				<div>
					<dt className="font-medium">{t("kept.removed")}</dt>
					<dd>
						<ul className="text-muted-foreground">
							{REMOVED_KINDS.map((k) => (
								<li key={k} data-testid={`inbox-removed-${k}`}>
									{t(`kept.kinds.${k}`)}: {removed[k]}
								</li>
							))}
						</ul>
					</dd>
				</div>
			</dl>
		</div>
	);
}

/**
 * One item opened from the inbox: its detail and the actions. Keyed by item id by the caller, so
 * switching items starts fresh. The detail is fetched by id, so the panel stays open after an
 * accept moves the item out of the current filter, with the draft that accept wrote.
 */
export function InboxItemPanel({ itemId, suites, onClose }: { itemId: string; suites: readonly InboxSuite[]; onClose: () => void }) {
	const { t } = useTranslation("bench");
	const detail = useGetInboxItemQuery(itemId);
	const [accept, accepted] = useAcceptInboxItemMutation();
	const [reject, rejected] = useRejectInboxItemMutation();
	const [snooze, snoozed] = useSnoozeInboxItemMutation();
	const [last, setLast] = useState<InboxAction | null>(null);

	if (detail.isLoading) return <BlockSkeleton rows={8} testId="inbox-detail-loading" />;
	if (detail.error || !detail.data) {
		return (
			<LoadError
				title={t("inbox.detail.loadError", { id: itemId })}
				error={detail.error}
				onRetry={() => void detail.refetch()}
				testId="inbox-detail-error"
			/>
		);
	}

	const lastResult = last === "accept" ? accepted : last === "reject" ? rejected : last === "snooze" ? snoozed : null;
	const draft = accepted.data?.item.id === itemId ? accepted.data.draft : undefined;
	const done =
		last === "reject" && rejected.data?.item.id === itemId
			? { action: "reject" as const, item: rejected.data.item }
			: last === "snooze" && snoozed.data?.item.id === itemId
				? { action: "snooze" as const, item: snoozed.data.item }
				: undefined;

	return (
		<div className="flex min-w-0 flex-col gap-3">
			<WhatIsKept redacted={detail.data.item.redacted} />
			<InboxItemDetailView
				detail={detail.data}
				onClose={onClose}
				actions={
					<>
						{draft && <InboxDraftResult draft={draft} />}
						{draft && <InboxDraftNext draft={draft} />}
						<InboxActions
							item={detail.data.item}
							session={detail.data.session}
							suites={suites}
							mode={detail.data.mode}
							submitting={accepted.isLoading || rejected.isLoading || snoozed.isLoading}
							submitError={lastResult?.isError ? getBenchErrorMessage(lastResult.error) : undefined}
							done={done}
							onAccept={(body) => {
								setLast("accept");
								void accept({ id: itemId, body });
							}}
							onReject={(body) => {
								setLast("reject");
								void reject({ id: itemId, body });
							}}
							onSnooze={(body) => {
								setLast("snooze");
								void snooze({ id: itemId, body });
							}}
						/>
					</>
				}
			/>
		</div>
	);
}