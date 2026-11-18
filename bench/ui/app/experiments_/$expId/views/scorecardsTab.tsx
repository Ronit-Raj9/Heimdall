import { ExperimentResearchScorecard } from "@/components/research/researchScorecard"; // W4 research with Tavily
import { ExperimentGatewayPolicy } from "@/components/routing/gatewayPolicyCard"; // W9 routing policy under test
import { ExperimentSupportScorecard } from "@/components/support/supportScorecard"; // W3 customer support

/** The Scorecards tab: support, research and routing summaries. Each card renders only when it applies. */
export function ScorecardsTab({ expId }: { expId: string }) {
	return (
		<div className="flex flex-col gap-6" data-testid="scorecards-screen">
			<ExperimentSupportScorecard expId={expId} />
			<ExperimentResearchScorecard expId={expId} />
			<ExperimentGatewayPolicy expId={expId} />
		</div>
	);
}