import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { GitCompareArrows } from "lucide-react";
import { useTranslation } from "react-i18next";

/** "Compare versions": opens the Diff tab of /agents, for one agent's two newest versions when agent is set. */
export function CompareVersionsLink({ agent, className }: { agent?: string; className?: string }) {
	const { t } = useTranslation("bench");
	return (
		<Link
			to="/agents"
			search={agent ? { tab: "diff", agent } : { tab: "diff" }}
			title={t("agentDiff.link.compareHint")}
			className={cn(
				"bg-background hover:bg-accent hover:text-accent-foreground focus-visible:ring-ring/50 inline-flex h-8 items-center gap-1.5 rounded-sm border px-3 text-sm font-medium focus-visible:ring-[3px] focus-visible:outline-none",
				className,
			)}
			data-testid={agent ? `agentdiff-compare-link-${agent}` : "agentdiff-compare-link"}
		>
			<GitCompareArrows className="size-4" aria-hidden="true" />
			{t("agentDiff.link.compare")}
		</Link>
	);
}