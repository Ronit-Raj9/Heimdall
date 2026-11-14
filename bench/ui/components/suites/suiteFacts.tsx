import { formatDateTime, shortHash } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { SuiteSummary } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, CircleX } from "lucide-react";
import { useTranslation } from "react-i18next";
import { CopyCodeBlock } from "./copyCodeBlock";
import { LockBadge, VerifierBadge } from "./suiteBadges";
import { leaderboardSuite } from "./suites.utils";

/** The suite's settings as a definition list, with a link to its leaderboard. */
export function SuiteFacts({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const none = <span className="text-muted-foreground">{t("suites.facts.none")}</span>;
	const facts: { label: string; value: React.ReactNode; testId: string }[] = [
		{ label: t("suites.facts.environment"), value: <code>{suite.environment}</code>, testId: "environment" },
		{ label: t("suites.facts.grader"), value: <code>{suite.grader}</code>, testId: "grader" },
		{ label: t("suites.facts.verifier"), value: <VerifierBadge format={suite.verifier_result} />, testId: "verifier" },
		{
			label: t("suites.facts.timeout"),
			value: suite.verifier_timeout_s ? t("suites.facts.seconds", { count: suite.verifier_timeout_s }) : none,
			testId: "timeout",
		},
		...(suite.pass_threshold !== null
			? [{ label: t("suites.facts.threshold"), value: <span className="tabular-nums">{suite.pass_threshold}</span>, testId: "threshold" }]
			: []),
		{ label: t("suites.facts.image"), value: <code className="break-all">{suite.base_image.tag}</code>, testId: "image" },
		{
			label: t("suites.facts.digest"),
			value: suite.base_image.digest ? <code title={suite.base_image.digest}>{shortHash(suite.base_image.digest)}</code> : none,
			testId: "digest",
		},
		{
			label: t("suites.facts.lockHash"),
			value: suite.lock_hash ? <code title={suite.lock_hash}>{shortHash(suite.lock_hash)}</code> : none,
			testId: "lock-hash",
		},
		{ label: t("suites.facts.lockedAt"), value: formatDateTime(suite.locked_at ?? undefined, locale) ?? none, testId: "locked-at" },
	];
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid="suites-facts">
			<div className="flex flex-wrap items-center gap-2">
				<LockBadge state={suite.lock_state} testId="suites-facts-lock" />
				<Link
					to="/leaderboard"
					search={{ suite: leaderboardSuite(suite) }}
					className="text-primary text-xs underline-offset-4 hover:underline"
					data-testid="suites-leaderboard-link"
				>
					{t("suites.facts.leaderboard")}
				</Link>
			</div>
			<dl className="grid min-w-0 grid-cols-1 gap-x-6 gap-y-2 text-xs sm:grid-cols-2 xl:grid-cols-3" aria-label={t("suites.facts.label")}>
				{facts.map((fact) => (
					<div key={fact.testId} className="flex min-w-0 flex-col gap-0.5" data-testid={`suites-fact-${fact.testId}`}>
						<dt className="text-muted-foreground">{fact.label}</dt>
						<dd className="text-foreground min-w-0 font-mono">{fact.value}</dd>
					</div>
				))}
			</dl>
		</div>
	);
}

/** What changed since the lock, and the commands that re-lock and re-admit. Nothing when the lock matches. */
export function SuiteDriftNotice({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("bench");
	if (suite.lock_state === "unknown" && suite.lock_error) {
		return (
			<div
				role="alert"
				className="border-chart-error/50 bg-chart-error/10 flex items-start gap-2 rounded-sm border px-3 py-2 text-xs"
				data-testid="suites-lock-error"
			>
				<CircleX className="text-chart-error-ink mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
				<span className="min-w-0 break-words">{t("suites.drift.lockError", { error: suite.lock_error })}</span>
			</div>
		);
	}
	if (suite.drift.length === 0) return null;
	return (
		<div
			className="border-chart-warning/50 bg-chart-warning/10 flex min-w-0 flex-col gap-2 rounded-sm border px-3 py-2"
			data-testid="suites-drift"
		>
			<div className="flex items-start gap-2">
				<AlertTriangle className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
				<div className="flex min-w-0 flex-col gap-0.5">
					<p className="text-foreground text-sm font-medium">{t("suites.drift.title")}</p>
					<p className="text-muted-foreground text-xs">{t("suites.drift.description")}</p>
				</div>
			</div>
			<ul className="flex min-w-0 flex-col gap-1 pl-6 text-xs">
				{suite.drift.map((d) => (
					<li
						key={`${d.scope}-${d.id}`}
						className="flex min-w-0 flex-wrap items-center gap-2"
						data-testid={`suites-drift-${d.scope}-${d.id}`}
					>
						<span className="font-mono">{t("suites.drift.row", { scope: d.scope, id: d.id, fields: d.fields.join(", ") })}</span>
						<span className="text-muted-foreground font-mono">
							{d.level} / {d.action}
						</span>
					</li>
				))}
			</ul>
			<p className="text-muted-foreground pl-6 text-xs">{t("suites.drift.fix")}</p>
			<CopyCodeBlock
				className="ml-6"
				code={`bifrost-bench suite lock ${suite.id}\nbifrost-bench suite admit ${suite.id}`}
				label={t("suites.drift.fix")}
				testId="suites-drift-fix"
			/>
		</div>
	);
}

/** A suite that failed to load: the loader's message, which names the file and field. */
export function BrokenSuite({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("bench");
	return (
		<div
			role="alert"
			className="border-chart-error/50 bg-chart-error/5 flex min-w-0 flex-col gap-2 rounded-sm border px-4 py-3"
			data-testid="suites-broken"
		>
			<p className="text-foreground flex items-center gap-2 text-sm font-medium">
				<CircleX className="text-chart-error-ink size-4 shrink-0" aria-hidden="true" />
				{t("suites.broken.title")}
			</p>
			<pre className="text-foreground min-w-0 overflow-x-auto font-mono text-xs break-all whitespace-pre-wrap">{suite.error}</pre>
			<p className="text-muted-foreground text-xs">{t("suites.broken.hint", { suite: suite.id })}</p>
		</div>
	);
}