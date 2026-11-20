import { clipText } from "@/components/runs/stepTimeline.utils";
import { ToneBadge } from "@/components/suites/suiteBadges";
import { cn } from "@/lib/utils";
import { CircleAlert, CircleCheck, Clock, Megaphone, ShieldCheck, Tag, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";
import { exitTone, knownExit, knownSource, knownStrength, strengthTone } from "./agents.utils";

// Small shared pieces of the Agents section. Agent-influenced strings (ids, roles, models, exit
// text the server did not classify) are clipped React text nodes: never HTML, never a translation key.

/** A value the run did not record: hatched "unknown", never zero, with what was missing as its title and accessible name. */
export function UnknownValue({ hint, testId, className }: { hint?: string; testId?: string; className?: string }) {
	const { t } = useTranslation("bench");
	const label = hint ?? t("runs.agents.unknownHint");
	return (
		<span
			className={cn(
				"bench-hatched text-muted-foreground inline-flex h-5 cursor-help items-center rounded-[4px] border border-dashed px-1.5 text-[11px] leading-none",
				className,
			)}
			title={label}
			aria-label={label}
			data-state="unknown"
			data-testid={testId}
		>
			{t("runs.agents.unknown")}
		</span>
	);
}

/** An agent id: monospace, clipped, the full id in the title. */
export function AgentId({ id, className, testId }: { id: string | undefined; className?: string; testId?: string }) {
	const { t } = useTranslation("bench");
	if (!id) {
		return (
			<span className={cn("text-muted-foreground italic", className)} data-testid={testId}>
				{t("runs.agents.noAgent")}
			</span>
		);
	}
	const clipped = clipText(id, 48);
	return (
		<span className={cn("text-foreground font-mono break-all", className)} title={clipText(id, 240)} data-testid={testId}>
			{clipped}
		</span>
	);
}

/** The agent's role as written by the run (lead, worker, or any other string): a quiet pill of plain text. */
export function RoleChip({ role, testId }: { role: string | undefined; testId?: string }) {
	if (!role) return null;
	return (
		<span
			className="bg-muted text-muted-foreground inline-flex w-fit shrink-0 items-center rounded-full border px-2 py-px text-[11px] font-medium whitespace-nowrap"
			data-testid={testId}
		>
			{clipText(role, 32)}
		</span>
	);
}

const STRENGTH_ICON: Record<string, LucideIcon> = { proven: ShieldCheck, declared: Tag, reported: Megaphone };

/**
 * How an agent is known: proven by a bench-minted key, declared in a header, or reported by the
 * agent itself (dashed, like agent-reported timeline rows). The source joins the hint.
 */
export function StrengthBadge({ strength, source, testId }: { strength: string | undefined; source?: string; testId?: string }) {
	const { t } = useTranslation("bench");
	const known = knownStrength(strength);
	const label = known ? t(`runs.agents.strength.${known}`) : clipText(strength, 20) || t("runs.agents.unknown");
	const sourceText = source ? sourceLabel(t, source) : "";
	const hint = [
		known ? t(`runs.agents.strengthHint.${known}`) : t("runs.agents.strengthHint.unknown"),
		sourceText && `${t("runs.agents.via")} ${sourceText}`,
	]
		.filter(Boolean)
		.join(". ");
	return (
		<ToneBadge
			tone={strengthTone(strength)}
			icon={known ? STRENGTH_ICON[known] : undefined}
			hint={hint}
			testId={testId}
			className={cn(known !== "proven" && "border-dashed")}
		>
			{label}
		</ToneBadge>
	);
}

type TFn = ReturnType<typeof useTranslation<"bench">>["t"];

/** A translated attribution source, or the raw value as clipped text. */
export function sourceLabel(t: TFn, source: string | undefined): string {
	const known = knownSource(source);
	return known ? t(`runs.agents.source.${known}`) : clipText(source, 32);
}

const EXIT_ICON: Record<string, LucideIcon> = { good: CircleCheck, bad: CircleAlert, warn: Clock };

/** How an agent ended. Completed, error and timeout are translated; "exit code n" and others are shown as text. */
export function ExitBadge({ exit, testId }: { exit: string | undefined; testId?: string }) {
	const { t } = useTranslation("bench");
	if (!exit) return null;
	const known = knownExit(exit);
	const tone = exitTone(exit);
	return (
		<ToneBadge tone={tone} icon={EXIT_ICON[tone]} hint={t("runs.agents.exitHint")} testId={testId}>
			{known ? t(`runs.agents.exit.${known}`) : clipText(exit, 32)}
		</ToneBadge>
	);
}