import { Button } from "@/components/ui/button";
import { Check, ClipboardCopy, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

type CopyState = "idle" | "copied" | "failed";

/** Copies the markdown built on click (the view as shown) and says whether it worked. */
export function CopyMarkdownButton({ build, disabled }: { build: () => string; disabled?: boolean }) {
	const { t } = useTranslation("bench");
	const [state, setState] = useState<CopyState>("idle");
	const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	useEffect(() => () => clearTimeout(timer.current), []);

	const copy = async () => {
		let next: CopyState = "failed";
		try {
			if (navigator.clipboard) {
				await navigator.clipboard.writeText(build());
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
		<Button variant="outline" size="sm" onClick={() => void copy()} disabled={disabled} dataTestId="leaderboard-copy-markdown">
			<Icon aria-hidden="true" />
			<span aria-live="polite">
				{state === "copied"
					? t("leaderboard.copy.copied")
					: state === "failed"
						? t("leaderboard.copy.failed")
						: t("leaderboard.copy.button")}
			</span>
		</Button>
	);
}