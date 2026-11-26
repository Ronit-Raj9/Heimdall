import { Button } from "@/components/ui/button";

/** True when a name taken from the address is not in the loaded list. False while the list loads or when there is no name (wave 4, N76). */
export function isUnknownName(known: readonly string[] | undefined, value: string | null | undefined): boolean {
	if (!known || value === null || value === undefined || value === "") return false;
	return !known.includes(value);
}

/** The note shown when a link names something that does not exist (wave 4, N76). The caller passes the translated text. */
export function UnknownLinkNote({ text, showAllLabel, onShowAll, testId }: { text: string; showAllLabel?: string; onShowAll?: () => void; testId: string }) {
	return (
		<div role="status" data-testid={testId} className="text-muted-foreground flex flex-wrap items-center gap-3 rounded-md border px-3 py-2 text-sm">
			<span>{text}</span>
			{onShowAll && showAllLabel && (
				<Button size="sm" variant="outline" onClick={onShowAll} dataTestId={`${testId}-show-all`}>
					{showAllLabel}
				</Button>
			)}
		</div>
	);
}
