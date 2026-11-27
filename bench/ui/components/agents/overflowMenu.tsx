import { Button } from "@/components/ui/button";
import { DropdownMenu, type DropdownMenuItem } from "@/components/ui/dropdownMenu";
import { MoreHorizontal, type LucideIcon } from "lucide-react";

export interface OverflowMenuItem {
	label: string;
	/** A route to open; leave it out and pass onSelect for an action. */
	to?: string;
	search?: Record<string, unknown>;
	params?: Record<string, string>;
	onSelect?: () => void;
	icon?: LucideIcon;
	/** Only for the item that opens a delete confirm; the confirm button carries the red. */
	destructive?: boolean;
	disabled?: boolean;
	disabledReason?: string;
	separatorBefore?: boolean;
	testId?: string;
}

/**
 * The "More" button of a page header: secondary actions and Delete live here (Spec 3). A thin
 * wrapper over the shared DropdownMenu, so it closes on a pick, Escape, a click outside or Back,
 * and the arrow keys move between items.
 */
export function OverflowMenu({ label, items, testId, className }: { label: string; items: OverflowMenuItem[]; testId?: string; className?: string }) {
	return (
		<DropdownMenu
			label={label}
			items={items as DropdownMenuItem[]}
			testId={testId}
			className={className}
			trigger={(props) => (
				<Button {...props} size="sm" variant="outline" aria-label={label} title={label} dataTestId={testId ? `${testId}-button` : undefined}>
					<MoreHorizontal aria-hidden="true" />
				</Button>
			)}
		/>
	);
}
