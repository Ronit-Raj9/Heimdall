import { cn } from "@/lib/utils";

/** The first reason that is a non-empty string, else null: firstReason(!name && t("needName"), !file && t("needFile")). */
export function firstReason(...reasons: (string | false | null | undefined)[]): string | null {
	for (const r of reasons) if (typeof r === "string" && r.trim() !== "") return r;
	return null;
}

/** The visible reason under a disabled button. Renders nothing when reason is empty. */
export function WhyDisabled({ reason, testId, id, className }: { reason: string | null | undefined; testId: string; id?: string; className?: string }) {
	if (!reason) return null;
	return (
		<p id={id ?? testId} data-testid={testId} className={cn("text-muted-foreground text-xs", className)}>
			{reason}
		</p>
	);
}
