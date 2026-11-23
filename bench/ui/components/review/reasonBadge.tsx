import { Badge } from "@/components/ui/badge";
import { useTranslation } from "react-i18next";
import "./hw07I18n";
import { isReviewReason } from "./review.utils";

/**
 * One queue reason as a badge. A reason this UI does not know yet shows as the server sent it.
 * `detail` names the safety checks that threw an `invalidated` run out (the queue item's `thrown_out_by`).
 */
export function ReasonBadge({ reason, testId, detail }: { reason: string; testId?: string; detail?: string[] }) {
	const { t } = useTranslation(["bench", "hw07"]);
	let label = reason;
	if (reason === "invalidated") {
		label =
			detail && detail.length > 0
				? t("hw07:review.reason.invalidatedBy", { detail: detail.join("; ") })
				: t("hw07:review.reason.invalidated");
	} else if (isReviewReason(reason)) {
		label = t(`review.reason.${reason}`);
	}
	return (
		<Badge variant={reason === "veto" ? "destructive" : "warning"} data-testid={testId} data-reason={reason}>
			{label}
		</Badge>
	);
}