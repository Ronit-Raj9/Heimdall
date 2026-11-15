import { cn } from "@/lib/utils";

/**
 * Heimdall's mark: a gate with a watchful eye under its arch. One colour (currentColor), so it
 * follows the theme. Decorative; the product name next to it is the accessible text.
 */
export function HeimdallMark({ className }: { className?: string }) {
	return (
		<svg
			viewBox="0 0 24 24"
			fill="none"
			stroke="currentColor"
			strokeWidth={1.75}
			strokeLinecap="round"
			strokeLinejoin="round"
			className={cn("size-5", className)}
			aria-hidden="true"
			focusable="false"
		>
			<path d="M4.5 20.5V11a7.5 7.5 0 0 1 15 0v9.5" />
			<path d="M2.5 20.5h19" />
			<path d="M8 14.25c1.15-1.5 2.5-2.25 4-2.25s2.85.75 4 2.25c-1.15 1.5-2.5 2.25-4 2.25s-2.85-.75-4-2.25Z" />
			<circle cx="12" cy="14.25" r="0.9" fill="currentColor" stroke="none" />
		</svg>
	);
}