import { cn } from "@/lib/utils";
import { Check, Copy } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

export interface CopyIdProps {
	/** The full id; it is what gets copied and what the tooltip shows. */
	value: string;
	/** Shorter text to show in place of the full id (a short hash). Defaults to the id, truncated by width. */
	display?: string;
	/** Hover text for the id; defaults to the full id. */
	title?: string;
	className?: string;
	testId?: string;
}

/**
 * A long id in monospace, cut to its column with an ellipsis, with a faint copy button that
 * brightens on hover and on keyboard focus. The full id stays in the title.
 */
export function CopyId({ value, display, title, className, testId }: CopyIdProps) {
	const { t } = useTranslation("bench");
	const [copied, setCopied] = useState(false);
	useEffect(() => {
		if (!copied) return;
		const timer = window.setTimeout(() => setCopied(false), 1400);
		return () => window.clearTimeout(timer);
	}, [copied]);

	const copy = () => {
		try {
			void navigator.clipboard?.writeText(value).then(
				() => setCopied(true),
				() => undefined,
			);
		} catch {
			// No clipboard (insecure context): the id is still selectable text.
		}
	};

	return (
		<span className={cn("group/copy inline-flex max-w-full min-w-0 items-center gap-1", className)} data-testid={testId}>
			<span className="min-w-0 truncate font-mono" title={title ?? value}>
				{display ?? value}
			</span>
			<button
				type="button"
				onClick={copy}
				aria-label={t("app.copyId", { id: value })}
				title={t("app.copyId", { id: value })}
				className="text-muted-foreground hover:text-foreground inline-flex size-6 shrink-0 cursor-pointer items-center justify-center rounded-md p-1 opacity-70 focus-visible:ring-(--focus) transition-opacity duration-100 group-hover/copy:opacity-100 focus-visible:opacity-100 focus-visible:ring-2 focus-visible:outline-none"
			>
				{copied ? <Check className="text-success-ink size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}
			</button>
		</span>
	);
}