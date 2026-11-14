import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { Check, ClipboardCopy, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

type CopyState = "idle" | "copied" | "failed";

export interface CopyCodeBlockProps {
	code: string;
	/** Names the block for the copy button, e.g. the step's explanation. */
	label: string;
	testId: string;
	className?: string;
}

/**
 * A command or file snippet with a copy button. The result is announced in a polite live region
 * that is always in the DOM, so screen readers hear "copied" without the focus moving.
 */
export function CopyCodeBlock({ code, label, testId, className }: CopyCodeBlockProps) {
	const { t } = useTranslation("bench");
	const [state, setState] = useState<CopyState>("idle");
	const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	useEffect(() => () => clearTimeout(timer.current), []);

	const copy = async () => {
		let next: CopyState = "failed";
		try {
			if (navigator.clipboard) {
				await navigator.clipboard.writeText(code);
				next = "copied";
			}
		} catch {
			// Clipboard blocked (no permission or an insecure origin): say so instead of failing silently.
		}
		setState(next);
		clearTimeout(timer.current);
		timer.current = setTimeout(() => setState("idle"), 2000);
	};

	const Icon = state === "copied" ? Check : state === "failed" ? X : ClipboardCopy;
	return (
		<div className={cn("bg-muted/60 relative flex min-w-0 items-start gap-2 rounded-sm border", className)} data-testid={testId}>
			<pre className="text-foreground min-w-0 flex-1 overflow-x-auto px-3 py-2 font-mono text-xs leading-relaxed whitespace-pre">
				<code>{code}</code>
			</pre>
			<Button
				variant="ghost"
				size="sm"
				className="m-1 shrink-0"
				onClick={() => void copy()}
				aria-label={t("suites.add.copyLabel", { step: label })}
				dataTestId={`${testId}-copy`}
			>
				<Icon aria-hidden="true" />
				<span className="hidden sm:inline">
					{state === "copied" ? t("suites.add.copied") : state === "failed" ? t("suites.add.copyFailed") : t("suites.add.copy")}
				</span>
			</Button>
			<span className="sr-only" role="status" aria-live="polite" data-testid={`${testId}-status`}>
				{state === "copied" ? t("suites.add.copiedAnnounce") : state === "failed" ? t("suites.add.copyFailedAnnounce") : ""}
			</span>
		</div>
	);
}