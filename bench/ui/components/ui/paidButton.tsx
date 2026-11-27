import { useConfirm } from "@/components/platform/confirmDialog";
import { Button } from "@/components/ui/button";
import { formatUsd } from "@/lib/money";
import { cn } from "@/lib/utils";
import { CircleDollarSign } from "lucide-react";
import { useBusyGuard } from "@/lib/hooks/useBusyGuard";
import { useId, type ComponentProps } from "react";
import { useTranslation } from "react-i18next";

type Translate = (key: string, opts?: Record<string, unknown>) => string;

export type PaidConfirmMode = "always" | "auto" | "never";

/**
 * Where the button sits. form (default): next to the form or panel that showed the cap and the
 * estimate. row: a row action in a list. retry: a Retry or rerun. runNow: a Run now or Start now.
 * Row, retry and runNow always ask, whatever the cap, because nothing next to them showed the cost.
 */
export type PaidButtonKind = "form" | "row" | "retry" | "runNow";

/** The one threshold (M1-51): a known cap at or under this many USD starts on one click under auto. */
export const PAID_ONE_CLICK_MAX_USD = 1;

export interface PaidButtonProps extends Omit<ComponentProps<typeof Button>, "onClick" | "children" | "dataTestId"> {
	/** What the button does, as a verb phrase: "Run failure test". */
	verb: string;
	/** The hard cap the server enforces, in USD; null when unknown (it uses the person's model key). */
	capUsd: number | null;
	/** The estimate in USD, shown when known. */
	estimateUsd?: number | null;
	/** About how many runs it starts. */
	estimateRuns?: number;
	onConfirm(): void | Promise<unknown>;
	/**
	 * When to ask first. auto (default): when the cap is over PAID_ONE_CLICK_MAX_USD, or unknown, or
	 * the kind is row, retry or runNow. Pass "always" for work with lasting effects (a schedule, a
	 * replay, an investigation) so it never starts on one click.
	 */
	confirm?: PaidConfirmMode;
	/** Where the button sits (see PaidButtonKind); default form. */
	kind?: PaidButtonKind;
	/** One more sentence for the dialog. */
	confirmText?: string;
	/**
	 * Replaces the cap part of the label when the server has no cap of its own (M5-5): a rerun says
	 * "Rerun (same budget as before)" instead of "(uses your model key)". The dialog keeps the cost line.
	 */
	capText?: string;
	/** Where the estimate shows: a line under the button (default) or the button's tooltip (row actions). */
	estimateAt?: "below" | "title";
	testId: string;
}

/**
 * True when a paid click must be confirmed first. auto: the cap is unknown or over
 * PAID_ONE_CLICK_MAX_USD, or the button is a row action, a Retry or a Run now.
 */
export function needsPaidConfirm(mode: PaidConfirmMode, capUsd: number | null, kind: PaidButtonKind = "form"): boolean {
	if (mode === "always") return true;
	if (mode === "never") return false;
	if (kind !== "form") return true;
	return capUsd === null || !Number.isFinite(capUsd) || capUsd > PAID_ONE_CLICK_MAX_USD;
}

/** The button label: "Run failure test, up to $5.00", or "... (uses your model key)" when no cap is known. */
export function paidLabel(t: Translate, verb: string, capUsd: number | null, capText?: string): string {
	if (capText) return `${verb} (${capText})`;
	return capUsd === null ? t("labelUnknown", { verb }) : t("label", { verb, cap: formatUsd(capUsd) });
}

/** The estimate line, or null when nothing is known. */
export function paidEstimate(t: Translate, runs: number | undefined, usd: number | null | undefined): string | null {
	const hasUsd = usd !== undefined && usd !== null && Number.isFinite(usd);
	const hasRuns = runs !== undefined && Number.isFinite(runs);
	if (hasRuns && hasUsd) return t("estimate", { runs, usd: formatUsd(usd) });
	if (hasRuns) return t("estimateRuns", { runs });
	if (hasUsd) return t("estimateUsd", { usd: formatUsd(usd) });
	return null;
}

/**
 * The one button for work that spends money (Spec 11): the label says it costs money and names
 * the cap, the estimate shows under it, and the click asks through the shared confirm with the
 * amount before anything starts (see needsPaidConfirm). It stays busy, with its label, while the
 * work is being started.
 */
export function PaidButton({
	verb,
	capUsd,
	estimateUsd,
	estimateRuns,
	onConfirm,
	confirm = "auto",
	kind = "form",
	confirmText,
	capText,
	estimateAt = "below",
	testId,
	disabled,
	isLoading,
	className,
	...rest
}: PaidButtonProps) {
	const { t } = useTranslation("paid");
	const ask = useConfirm();
	// One start per press: a second click while the work is being started does nothing (M7-73).
	const { busy, run } = useBusyGuard();
	const estimateId = useId();
	const tr = t as unknown as Translate;
	const label = paidLabel(tr, verb, capUsd, capText);
	const estimate = paidEstimate(tr, estimateRuns, estimateUsd);

	const click = () =>
		run(async () => {
			if (needsPaidConfirm(confirm, capUsd, kind)) {
				const ok = await ask([estimate, confirmText].filter(Boolean).join(" ") || tr("costsMoney"), {
					title: tr("confirm.title", { verb }),
					confirmLabel: capUsd === null ? tr("confirm.okUnknown", { verb }) : tr("confirm.ok", { verb, cap: formatUsd(capUsd) }),
					costUsd: capUsd,
				});
				if (!ok) return;
			}
			await onConfirm();
		});

	const button = (
		<Button
			{...rest}
			type="button"
			className={className}
			disabled={disabled}
			isLoading={isLoading || busy}
			onClick={() => void click()}
			title={estimateAt === "title" ? (estimate ?? undefined) : rest.title}
			aria-describedby={estimate && estimateAt === "below" ? estimateId : rest["aria-describedby"]}
			data-paid="true"
			dataTestId={testId}
		>
			<CircleDollarSign aria-hidden="true" />
			{label}
		</Button>
	);
	if (!estimate || estimateAt === "title") return button;
	return (
		<span className="inline-flex flex-col items-end gap-1">
			{button}
			<span id={estimateId} className={cn("text-muted-foreground text-xs tabular-nums")} data-testid={`${testId}-estimate`}>
				{estimate}
			</span>
		</span>
	);
}