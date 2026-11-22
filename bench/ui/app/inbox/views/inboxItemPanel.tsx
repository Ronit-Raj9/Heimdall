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
	);
}