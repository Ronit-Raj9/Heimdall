import { Badge } from "@/components/ui/badge";
import { useTranslation } from "react-i18next";
import { isReviewReason } from "./review.utils";

/** One queue reason as a badge. A reason this UI does not know yet shows as the server sent it. */
export function ReasonBadge({ reason, testId }: { reason: string; testId?: string }) {
	const { t } = useTranslation("bench");
	return (
		<Badge variant={reason === "veto" ? "destructive" : "warning"} data-testid={testId} data-reason={reason}>
			{isReviewReason(reason) ? t(`review.reason.${reason}`) : reason}
		</Badge>
	);
}