import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { Check, Copy, KeyRound } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** Copies text to the clipboard; reports success for a moment. Quietly does nothing without a clipboard. */
export function useCopy(resetMs = 1600): [boolean, (text: string) => void] {
	const [copied, setCopied] = useState(false);
	useEffect(() => {
		if (!copied) return;
		const timer = window.setTimeout(() => setCopied(false), resetMs);
		return () => window.clearTimeout(timer);
	}, [copied, resetMs]);
	const copy = (text: string) => {
		try {
			void navigator.clipboard?.writeText(text).then(
				() => setCopied(true),
				() => undefined,
			);
		} catch {
			// No clipboard (insecure context): the value is selectable text.
		}
	};
	return [copied, copy];
}

/** A block of monospace text with a copy button: a token, a link or a command. */
export function CopyBlock({ value, label, testId, className }: { value: string; label: string; testId: string; className?: string }) {
	const { t } = useTranslation("platform");
	const [copied, copy] = useCopy();
	return (
		<div className={cn("bench-mono-block flex items-start gap-2 py-2 pr-2 pl-3", className)} data-testid={testId}>
			<code className="text-foreground min-w-0 flex-1 text-[12.5px] leading-6 break-all select-all" aria-label={label}>
				{value}
			</code>
			<Button
				type="button"
				variant="outline"
				size="sm"
				className="h-7 shrink-0 gap-1.5 px-2 text-xs"
				onClick={() => copy(value)}
				aria-label={`${t("secret.copy")}: ${label}`}
				dataTestId={`${testId}-copy`}
			>
				{copied ? (
					<Check className="text-chart-success-ink size-3.5" aria-hidden="true" />
				) : (
					<Copy className="size-3.5" aria-hidden="true" />
				)}
				{copied ? t("secret.copied") : t("secret.copy")}
			</Button>
		</div>
	);
}

export interface OneTimeSecretProps {
	title: string;
	/** The secret itself: shown here once, never again. */
	secret: string;
	secretLabel: string;
	/** More to show under the secret: a usage line, a command, a link. */
	children?: ReactNode;
	onDone: () => void;
	testId: string;
}

/**
 * A just-created secret (API token, runner token, invitation link), shown once with a copy button.
 * Closing it drops the value from the page; the server keeps only its hash.
 */
export function OneTimeSecret({ title, secret, secretLabel, children, onDone, testId }: OneTimeSecretProps) {
	const { t } = useTranslation("platform");
	return (
		<section
			className="border-chart-warning/55 bg-chart-warning/[0.06] flex flex-col gap-3 rounded-lg border p-4"
			aria-label={title}
			data-testid={testId}
		>
			<div className="flex flex-wrap items-center gap-2">
				<span className="bg-chart-warning/20 text-chart-warning-ink flex size-7 items-center justify-center rounded-md">
					<KeyRound className="size-4" aria-hidden="true" />
				</span>
				<h3 className="text-foreground text-sm font-semibold">{title}</h3>
				<span
					className="border-chart-warning/60 text-chart-warning-ink rounded-sm border px-1.5 py-px font-mono text-[10.5px] font-semibold tracking-[0.08em] uppercase"
					data-testid={`${testId}-once`}
				>
					{t("secret.once")}
				</span>
			</div>
			<p className="text-muted-foreground text-xs">{t("secret.onceHint")}</p>
			<CopyBlock value={secret} label={secretLabel} testId={`${testId}-value`} />
			{children}
			<div className="flex flex-wrap items-center justify-between gap-2 pt-1">
				<span className="text-muted-foreground text-[11px]">{t("secret.hidden")}</span>
				<Button type="button" size="sm" onClick={onDone} dataTestId={`${testId}-done`}>
					{t("secret.done")}
				</Button>
			</div>
		</section>
	);
}