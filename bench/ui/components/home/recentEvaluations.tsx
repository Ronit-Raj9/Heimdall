import { formatRelative, formatWhen } from "@/components/platform/platform.utils";
import { getLocale } from "@/lib/i18n";
import type { Evaluation, EvaluationStatus } from "@/lib/store/apis/evaluationsApi";
import "@/lib/types/homeI18n";
import { isTestEvaluation } from "@/app/evaluations/views/evaluation.utils";
import { StatusPill } from "@/components/ui/statusPill";
import { Link } from "@tanstack/react-router";
import { Award, ChevronRight } from "lucide-react";
import { useTranslation } from "react-i18next";

const STATUSES: readonly EvaluationStatus[] = ["queued", "running", "done", "failed", "cancelled"];

/** Who an evaluation tested: "codex v2 · gpt-x" for one arm, "A vs B" labels for two. */
export function evaluationWho(ev: Pick<Evaluation, "spec">): string {
	const arms = ev.spec?.arms ?? [];
	const one = (arm: (typeof arms)[number]) =>
		[arm.version ? `${arm.agent} v${arm.version}` : arm.agent, arm.model].filter(Boolean).join(" · ");
	return arms.map(one).join(" vs ");
}

/** The newest evaluations: name, who was tested, how many benchmarks, status and when. */
export function RecentEvaluations({ evaluations }: { evaluations: readonly Evaluation[] }) {
	// Home hides the project's own test evaluations (M1-18); the evaluations list has the switch to show them.
	const shown = evaluations.filter((ev) => !isTestEvaluation(ev));
	return (
		<ul className="divide-border flex flex-col divide-y" data-testid="home-evaluations-list">
			{shown.map((ev) => (
				<EvaluationRow key={ev.id} ev={ev} />
			))}
		</ul>
	);
}

function EvaluationRow({ ev }: { ev: Evaluation }) {
	const { t } = useTranslation("home");
	const locale = getLocale();
	const count = ev.children?.length || ev.spec?.benchmarks?.length || 0;
	const status = STATUSES.includes(ev.status) ? ev.status : "queued";
	return (
		<li data-testid={`home-evaluation-${ev.id}`}>
			<Link
				to={`/evaluations/${encodeURIComponent(ev.id)}` as never}
				className="group hover:bg-muted focus-visible:outline-ring flex min-w-0 items-center gap-3 px-4 py-3 transition-colors focus-visible:outline-2 focus-visible:-outline-offset-2 sm:px-5"
				title={ev.id}
			>
				<span className="bg-muted text-muted-foreground flex size-8 shrink-0 items-center justify-center rounded-lg" aria-hidden="true">
					<Award className="size-4" />
				</span>
				{/* Phones: the status sits under the name, so the name gets the row's width (Q-120). */}
				<span className="flex min-w-0 flex-1 flex-col gap-1 sm:flex-row sm:items-center sm:gap-3">
					<span className="flex min-w-0 flex-1 flex-col gap-0.5">
						{/* Phones: names and captions wrap to two lines instead of being cut (M2-15). */}
						<span className="text-foreground line-clamp-2 text-sm font-medium break-words sm:line-clamp-1">
							{ev.spec?.name?.trim() || t("evaluations.untitled")}
						</span>
						<span className="text-muted-foreground line-clamp-2 text-xs break-words sm:line-clamp-1">
							{evaluationWho(ev) || "-"}
							{" · "}
							{t("evaluations.benchmarks", { count })}
						</span>
					</span>
					{/* The shared status pill: icon plus the one status word (Spec 4). */}
					<StatusPill raw={status} className="shrink-0 self-start sm:self-auto" data-status={status} testId={`home-evaluation-status-${ev.id}`} />
				</span>
				<span className="text-muted-foreground hidden w-24 shrink-0 text-right text-xs whitespace-nowrap tabular-nums sm:block">
					{ev.created_at ? (
						<time dateTime={ev.created_at} title={formatWhen(ev.created_at, locale) ?? undefined}>
							{formatRelative(ev.created_at, locale)}
						</time>
					) : null}
				</span>
				<ChevronRight
					className="text-muted-foreground size-4 shrink-0 transition-transform group-hover:translate-x-0.5 motion-reduce:transition-none"
					aria-hidden="true"
				/>
			</Link>
		</li>
	);
}
