import { InlineError } from "@/components/runs/screenStates";
import { clipText } from "@/components/runs/stepTimeline.utils";
import { ToneBadge } from "@/components/suites/suiteBadges";
import { useGetRunA2AQuery } from "@/lib/store/apis/runA2AApi";
import type { A2AEvidence } from "@/lib/types/runA2A";
import { useTranslation } from "react-i18next";
import "./ufl4I18n";

const STRENGTH_TONE = { witnessed: "good", partial: "warn", unwitnessed: "bad" } as const;

/**
 * The agent gateway witness of an a2a run, fetched by run id. Renders nothing for any other run
 * (available false) and while loading; a failed request shows the server's reason.
 */
export function RunA2APanel({ runId }: { runId: string }) {
	const { t } = useTranslation("ufl4");
	const q = useGetRunA2AQuery(runId);
	if (q.isError) return <InlineError error={q.error} title={t("a2a.loadError")} onRetry={() => void q.refetch()} testId="run-a2a-error" />;
	if (!q.data?.available || !q.data.a2a) return null;
	return <A2AWitness a2a={q.data.a2a} />;
}

export function A2AWitness({ a2a }: { a2a: A2AEvidence }) {
	const { t } = useTranslation("ufl4");
	const strength = (a2a.strength in STRENGTH_TONE ? a2a.strength : "unwitnessed") as keyof typeof STRENGTH_TONE;
	const hops = a2a.hops ?? [];
	const linked = a2a.linked ?? [];
	const ag = a2a.agreement;
	const answer = ag?.answer_matches === undefined ? "unknown" : ag.answer_matches ? "agrees" : "differs";
	return (
		<section className="flex min-w-0 flex-col gap-2 rounded-md border p-3 text-xs" data-testid="run-a2a">
			<div className="flex flex-wrap items-center gap-2">
				<p className="text-sm font-medium">{t("a2a.title")}</p>
				<ToneBadge tone={STRENGTH_TONE[strength]}>{t(`a2a.strength.${strength}`)}</ToneBadge>
				{a2a.gateway ? <span className="text-muted-foreground">{t("a2a.gateway", { version: clipText(a2a.gateway, 40) })}</span> : null}
			</div>
			{a2a.reason ? <p className="text-muted-foreground">{clipText(a2a.reason, 200)}</p> : null}
			<div data-testid="run-a2a-hops">
				<p className="font-medium">{t("a2a.hops")}</p>
				{hops.length === 0 ? (
					<p className="text-muted-foreground">{t("a2a.noHops")}</p>
				) : (
					<ul className="flex flex-col gap-0.5">
						{hops.map((h) => (
							<li key={h.operation_id} className="break-words">
								{clipText(h.from, 40)} to {clipText(h.to, 40)}
								{h.task_id ? <span className="text-muted-foreground"> · {clipText(h.task_id, 40)}</span> : null}
							</li>
						))}
					</ul>
				)}
			</div>
			{linked.length > 0 ? (
				<div data-testid="run-a2a-linked">
					<p className="font-medium">{t("a2a.linked")}</p>
					<ul className="flex flex-col gap-0.5">
						{linked.map((r) => (
							<li key={`${r.kind}-${r.id}`} className="break-words">
								{r.kind.toUpperCase()} {clipText(r.id, 40)}
								{r.member ? ` · ${clipText(r.member, 30)}` : ""} ·{" "}
								<span className="text-muted-foreground">{r.on_run_key ? t("a2a.onRunKey") : t("a2a.otherKey")}</span>
							</li>
						))}
					</ul>
				</div>
			) : null}
			{ag ? (
				<div className="flex flex-wrap items-center gap-2" data-testid="run-a2a-agreement">
					<span className="font-medium">{t("a2a.agreement")}</span>
					<ToneBadge tone={answer === "agrees" ? "good" : answer === "differs" ? "bad" : "neutral"}>{t(`a2a.${answer}`)}</ToneBadge>
					{ag.launcher_state || ag.gateway_state ? (
						<span className="text-muted-foreground">
							{t("a2a.states", { launcher: clipText(ag.launcher_state, 20) || "-", gateway: clipText(ag.gateway_state, 20) || "-" })}
						</span>
					) : null}
				</div>
			) : null}
		</section>
	);
}
