import { StatusPill } from "@/components/ui/statusPill";
import type { Outcome } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";
import { outcomeTone } from "./runs.utils";

// The verifier's score as the shared pill tone: pass 1, the zero outcomes 0, the rest unknown (M5-63).
const PILL_TONE = { pass: "ok", zero: "bad", unknown: "neutral" } as const;

/** A run outcome as the shared status pill, coloured by how the verifier scores it. */
export function OutcomeBadge({ outcome, className, testId }: { outcome: Outcome; className?: string; testId?: string }) {
	const { t } = useTranslation("bench");
	return (
		<StatusPill
			raw={outcome}
			tone={PILL_TONE[outcomeTone(outcome)]}
			testId={testId ?? `outcome-${outcome}`}
			data-outcome={outcome}
			className={className}
		>
			{t(`runs.outcome.${outcome}`)}
		</StatusPill>
	);
}
