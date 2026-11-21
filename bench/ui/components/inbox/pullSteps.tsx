import type { PullCheck, PullStep } from "@/lib/types/liveinbox";
import { useTranslation } from "react-i18next";

/** The label of a step's link: Settings > Secrets, Models & keys, or the setup form on this page. */
function linkKey(link: string): "pull.steps.openSecrets" | "pull.steps.openModels" | "pull.steps.openForm" {
	if (link.startsWith("/settings")) return "pull.steps.openSecrets";
	if (link.startsWith("/models")) return "pull.steps.openModels";
	return "pull.steps.openForm";
}

/** One step: what is missing and the button that fixes it. */
export function PullStepRow({ step }: { step: PullStep }) {
	const { t } = useTranslation("liveinbox");
	return (
		<li className="flex flex-wrap items-center gap-2" data-testid="pull-step">
			<span className="text-foreground">{step.action}</span>
			<span className="text-muted-foreground">({step.problem})</span>
			{step.link && (
				<a
					href={step.link}
					className="text-primary font-medium underline underline-offset-2"
					data-testid={step.link.startsWith("/settings") ? "pull-step-secrets" : step.link.startsWith("/models") ? "pull-step-models" : "pull-step-form"}
				>
					{t(linkKey(step.link))}
				</a>
			)}
		</li>
	);
}

/** What a pull still needs, each with a link; nothing when ready. */
export function PullSteps({ check, testId }: { check: PullCheck | undefined; testId: string }) {
	const { t } = useTranslation("liveinbox");
	if (!check || check.ready || check.steps.length === 0) return null;
	return (
		<div className="flex flex-col gap-1 rounded-md border border-dashed p-3 text-xs" role="note" data-testid={testId}>
			<span className="text-foreground font-medium">{t("pull.steps.title")}</span>
			<ol className="flex list-decimal flex-col gap-1 pl-4">
				{check.steps.map((s, i) => (
					<PullStepRow key={`${s.link ?? ""}-${s.secret ?? ""}-${i}`} step={s} />
				))}
			</ol>
		</div>
	);
}
