import type { LockKind, SuiteSummary } from "@/lib/types/bench";
import { Fingerprint, ShieldAlert } from "lucide-react";
import { useTranslation } from "react-i18next";
import { ToneBadge } from "./suiteBadges";

/**
 * Whether suite.lock binds every evaluator setting (fingerprinted) or only the files it hashed
 * (legacy), with what that means. Nothing for an unlocked suite.
 */
export function LockKindBadge({ kind, testId }: { kind?: LockKind; testId?: string }) {
	const { t } = useTranslation("bench");
	if (!kind) return null;
	return (
		<ToneBadge
			tone={kind === "fingerprinted" ? "good" : "warn"}
			icon={kind === "fingerprinted" ? Fingerprint : ShieldAlert}
			hint={t(`suites.lockKind.${kind}Hint`)}
			testId={testId}
		>
			{t(`suites.lockKind.${kind}`)}
		</ToneBadge>
	);
}

/** What no longer matches the recorded lock fingerprints: scope, id, part and the settings or images that differ. */
export function FingerprintDriftNotice({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("bench");
	const drift = suite.fingerprint_drift ?? [];
	if (drift.length === 0) return null;
	return (
		<div
			role="alert"
			className="border-chart-error/50 bg-chart-error/10 flex min-w-0 flex-col gap-2 rounded-sm border px-3 py-2"
			data-testid="suites-fingerprint-drift"
		>
			<div className="flex items-start gap-2">
				<ShieldAlert className="text-chart-error-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
				<div className="flex min-w-0 flex-col gap-0.5">
					<p className="text-foreground text-sm font-medium">{t("suites.fingerprintDrift.title")}</p>
					<p className="text-muted-foreground text-xs">{t("suites.fingerprintDrift.description")}</p>
				</div>
			</div>
			<ul className="flex min-w-0 flex-col gap-1 pl-6 text-xs">
				{drift.map((d) => (
					<li
						key={`${d.scope}-${d.id}-${d.part}`}
						className="min-w-0 font-mono break-words"
						data-testid={`suites-fingerprint-drift-${d.scope}-${d.id}-${d.part}`}
					>
						{d.fields && d.fields.length > 0
							? t("suites.fingerprintDrift.rowFields", { scope: d.scope, id: d.id, part: d.part, fields: d.fields.join(", ") })
							: t("suites.fingerprintDrift.row", { scope: d.scope, id: d.id, part: d.part })}
					</li>
				))}
			</ul>
		</div>
	);
}