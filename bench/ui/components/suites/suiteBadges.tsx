import type { SuiteLockState, SuiteTask, VerifierResultFormat } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { CircleAlert, CircleCheck, CircleDashed, CircleX, Lock, LockOpen, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";
import "./uf6aI18n";
import { ADMISSION_TONE, LOCK_TONE, TONE_CLASS, admissionState, type AdmissionState, type Tone } from "./suites.utils";

/** A small labelled pill in one of the chart tones. The hint is its title and part of its accessible name. */
export function ToneBadge({
	tone,
	icon: Icon,
	children,
	hint,
	testId,
	className,
}: {
	tone: Tone;
	icon?: LucideIcon;
	children: React.ReactNode;
	hint?: string;
	testId?: string;
	className?: string;
}) {
	return (
		<span
			className={cn(
				"inline-flex w-fit shrink-0 items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium whitespace-nowrap",
				TONE_CLASS[tone],
				className,
			)}
			title={hint}
			data-testid={testId}
			data-tone={tone}
		>
			{Icon && <Icon className="size-3 shrink-0" aria-hidden="true" />}
			{children}
			{hint && <span className="sr-only">: {hint}</span>}
		</span>
	);
}

const LOCK_ICON: Record<SuiteLockState, LucideIcon> = { locked: Lock, unlocked: LockOpen, stale: CircleAlert, unknown: CircleX };

/** Locked, unlocked, stale or unknown, with what it means. */
export function LockBadge({ state, testId }: { state: SuiteLockState; testId?: string }) {
	const { t } = useTranslation("bench");
	return (
		<ToneBadge tone={LOCK_TONE[state]} icon={LOCK_ICON[state]} hint={t(`suites.lock.${state}Hint`)} testId={testId}>
			{t(`suites.lock.${state}`)}
		</ToneBadge>
	);
}

/** JUnit XML, reward file or exit code. */
export function VerifierBadge({ format, testId }: { format: VerifierResultFormat; testId?: string }) {
	const { t } = useTranslation("bench");
	return (
		<ToneBadge tone="neutral" hint={t(`suites.verifier.${format}Hint`)} testId={testId}>
			{t(`suites.verifier.${format}`)}
		</ToneBadge>
	);
}

const ADMISSION_ICON: Record<AdmissionState, LucideIcon> = {
	admitted: CircleCheck,
	stale: CircleAlert,
	draft: CircleX,
	unchecked: CircleDashed,
	not_locked: CircleDashed,
	quarantined: CircleAlert,
	retired: CircleDashed,
};

/** M6-2: states whose label and hint come from the lane 6a texts ("Check failed", "Not checked yet"). */
const UF6A_STATES = new Set<AdmissionState>(["draft", "unchecked"]);

/** The label and hint of an admission state. */
export function useAdmissionLabel(): (state: AdmissionState, hint?: boolean) => string {
	const { t } = useTranslation("bench");
	const { t: tf } = useTranslation("uf6a");
	const bench = t as unknown as (key: string) => string;
	const lane = tf as unknown as (key: string) => string;
	return (state, hint = false) => {
		const suffix = hint ? "Hint" : "";
		return UF6A_STATES.has(state) ? lane(`status.${state}${suffix}`) : bench(`suites.status.${state}${suffix}`);
	};
}

/** A task's admission state, with what it means. */
export function AdmissionBadge({ task, testId }: { task: SuiteTask; testId?: string }) {
	const label = useAdmissionLabel();
	const state = admissionState(task);
	return (
		<ToneBadge tone={ADMISSION_TONE[state]} icon={ADMISSION_ICON[state]} hint={label(state, true)} testId={testId}>
			{label(state)}
		</ToneBadge>
	);
}