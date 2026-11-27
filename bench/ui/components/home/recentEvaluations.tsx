import { formatRelative, formatWhen } from "@/components/platform/platform.utils";
import { getLocale } from "@/lib/i18n";
import type { Evaluation, EvaluationStatus } from "@/lib/store/apis/evaluationsApi";
import "@/lib/types/homeI18n";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { Award, ChevronRight } from "lucide-react";
import { useTranslation } from "react-i18next";

const STATUS_CLASS: Record<EvaluationStatus, string> = {
	queued: "bg-muted text-muted-foreground",
	running: "bg-blue-500/10 text-blue-700 dark:text-blue-300",
	done: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
	failed: "bg-red-500/10 text-red-700 dark:text-red-300",
	cancelled: "bg-muted text-muted-foreground",
};

/** Who an evaluation tested: "codex v2 · gpt-x" for one arm, "A vs B" labels for two. */
export function evaluationWho(ev: Pick<Evaluation, "spec">): string {
	const arms = ev.spec?.arms ?? [];
	const one = (arm: (typeof arms)[number]) =>
		[arm.version ? `${arm.agent} v${arm.version}` : arm.agent, arm.model].filter(Boolean).join(" · ");
	return arms.map(one).join(" vs ");
}

/** The newest evaluations: name, who was tested, how many benchmarks, status and when. */
export function RecentEvaluations({ evaluations }: { evaluations: readonly Evaluation[] }) {
	return (
		<ul className="divide-border/70 flex flex-col divide-y" data-testid="home-evaluations-list">
			{evaluations.map((ev) => (
				<EvaluationRow key={ev.id} ev={ev} />
			))}
		</ul>
	);
}

function EvaluationRow({ ev }: { ev: Evaluation }) {
	const { t } = useTranslation("home");
	const locale = getLocale();
	const count = ev.children?.length || ev.spec?.benchmarks?.length || 0;
	const status = ev.status in STATUS_CLASS ? ev.status : "queued";
	return (
		<li data-testid={`home-evaluation-${ev.id}`}>
			<Link
				to={`/evaluations/${encodeURIComponent(ev.id)}` as never}
				className="group hover:bg-accent/50 focus-visible:bg-accent/50 flex min-w-0 items-center gap-3 px-4 py-3 transition-colors focus-visible:outline-none sm:px-5"
				title={ev.id}
			>
				<span className="bg-muted text-muted-foreground flex size-8 shrink-0 items-center justify-center rounded-lg" aria-hidden="true">
					<Award className="size-4" />
				</span>
				<span className="flex min-w-0 flex-1 flex-col gap-0.5">
					<span className="text-foreground line-clamp-2 text-sm font-medium break-words">{ev.spec?.name?.trim() || t("evaluations.untitled")}</span>
					<span className="text-muted-foreground truncate text-xs">
						{evaluationWho(ev) || "-"}
						{" · "}
						{t("evaluations.benchmarks", { count })}
					</span>
				</span>
				<span
					className={cn("inline-flex h-5 shrink-0 items-center rounded-full px-2 text-xs font-medium", STATUS_CLASS[status])}
					data-status={status}
					data-testid={`home-evaluation-status-${ev.id}`}
				>
					{t(`evaluations.status.${status}`)}
				</span>
				<span className="text-muted-foreground hidden w-20 shrink-0 text-right text-xs tabular-nums sm:block">
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
