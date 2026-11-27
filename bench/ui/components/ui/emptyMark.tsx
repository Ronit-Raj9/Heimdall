import { cn } from "@/lib/utils";

export interface EmptyMarkProps {
	/** What the empty cell means, read by screen readers ("not run", "not applicable"). */
	label: string;
	/** Longer explanation shown on hover. */
	hint?: string;
	className?: string;
	testId?: string;
}

/**
 * A quiet mark for a cell with nothing to show: a short rule instead of repeated grey words, so
 * a wide table reads by its numbers. The meaning stays in the accessible name and the tooltip,
 * and the mark can never be mistaken for a zero.
 */
export function EmptyMark({ label, hint, className, testId }: EmptyMarkProps) {
	return (
		<span
			data-slot="empty-mark"
			className={cn("text-muted-foreground inline-flex cursor-default items-center", className)}
			title={hint ?? label}
			data-testid={testId}
		>
			<span aria-hidden="true" className="inline-block h-px w-3 rounded-full bg-current opacity-70" />
			<span className="sr-only">{label}</span>
		</span>
	);
}