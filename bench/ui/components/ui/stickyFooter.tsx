import { cn } from "@/lib/utils";
import { useEffect, useRef, useState, type ReactNode } from "react";

/**
 * The bar that keeps a long form's summary and primary button in view (M1-50): sticks to the
 * bottom of the scrolling area, buttons right aligned; on phones it wraps.
 *
 * `phone="fixed"` pins the bar to the bottom of the window below sm (round 3). The bar then leaves
 * a spacer of its own measured height in the page, so the page always has bottom padding as tall
 * as the bar and the last controls can scroll out from under it.
 */
export function StickyFooter({
	children,
	className,
	testId,
	phone = "sticky",
}: {
	children: ReactNode;
	className?: string;
	testId?: string;
	phone?: "sticky" | "fixed";
}) {
	const bar = useRef<HTMLDivElement>(null);
	const [height, setHeight] = useState(0);
	useEffect(() => {
		const el = bar.current;
		if (phone !== "fixed" || !el || typeof ResizeObserver === "undefined") return;
		const update = () => setHeight(Math.ceil(el.getBoundingClientRect().height));
		update();
		const observer = new ResizeObserver(update);
		observer.observe(el);
		return () => observer.disconnect();
	}, [phone]);
	const body = (
		<div
			ref={bar}
			className={cn(
				"bg-card bottom-0 flex flex-wrap items-center justify-end gap-2 border-t px-4 py-3",
				phone === "fixed" ? "fixed inset-x-0 z-20 pb-[max(0.75rem,env(safe-area-inset-bottom))] sm:sticky sm:z-10" : "sticky z-10",
				className,
			)}
			data-testid={testId ?? "sticky-footer"}
		>
			{children}
		</div>
	);
	if (phone !== "fixed") return body;
	return (
		<>
			<div aria-hidden="true" className="shrink-0 sm:hidden" style={{ height }} data-testid="sticky-footer-spacer" />
			{body}
		</>
	);
}
