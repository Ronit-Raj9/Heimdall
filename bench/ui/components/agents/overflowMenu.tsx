import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { MoreHorizontal, type LucideIcon } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";

export interface OverflowMenuItem {
	label: string;
	to: string;
	search?: Record<string, unknown>;
	params?: Record<string, string>;
	icon?: LucideIcon;
	testId?: string;
}

/**
 * A "more actions" button that opens a short list of links. Closes on a pick, Escape or a click
 * outside, and moves focus back to the button on Escape.
 */
export function OverflowMenu({ label, items, testId, className }: { label: string; items: OverflowMenuItem[]; testId?: string; className?: string }) {
	const [open, setOpen] = useState(false);
	const root = useRef<HTMLDivElement>(null);
	const button = useRef<HTMLButtonElement>(null);
	const menuId = useId();

	useEffect(() => {
		if (!open) return;
		const onDown = (e: MouseEvent) => {
			if (root.current && !root.current.contains(e.target as Node)) setOpen(false);
		};
		const onKey = (e: KeyboardEvent) => {
			if (e.key === "Escape") {
				setOpen(false);
				button.current?.focus();
			}
		};
		document.addEventListener("mousedown", onDown);
		document.addEventListener("keydown", onKey);
		return () => {
			document.removeEventListener("mousedown", onDown);
			document.removeEventListener("keydown", onKey);
		};
	}, [open]);

	return (
		<div ref={root} className={cn("relative", className)} data-testid={testId}>
			<Button
				ref={button}
				type="button"
				size="sm"
				variant="outline"
				aria-label={label}
				title={label}
				aria-haspopup="menu"
				aria-expanded={open}
				aria-controls={open ? menuId : undefined}
				onClick={() => setOpen((v) => !v)}
				dataTestId={testId ? `${testId}-button` : undefined}
			>
				<MoreHorizontal aria-hidden="true" />
			</Button>
			{open && (
				<ul
					id={menuId}
					role="menu"
					aria-label={label}
					className="bg-popover text-popover-foreground absolute right-0 z-50 mt-1 flex min-w-56 flex-col rounded-lg border p-1 shadow-md"
					data-testid={testId ? `${testId}-list` : undefined}
				>
					{items.map((item) => {
						const Icon = item.icon;
						return (
							<li key={item.label} role="none">
								<Link
									role="menuitem"
									to={item.to as never}
									params={item.params as never}
									search={item.search as never}
									onClick={() => setOpen(false)}
									className="hover:bg-muted focus-visible:bg-muted flex items-center gap-2 rounded-md px-2.5 py-1.5 text-sm outline-none"
									data-testid={item.testId}
								>
									{Icon && <Icon className="text-muted-foreground size-4" aria-hidden="true" />}
									{item.label}
								</Link>
							</li>
						);
					})}
				</ul>
			)}
		</div>
	);
}
