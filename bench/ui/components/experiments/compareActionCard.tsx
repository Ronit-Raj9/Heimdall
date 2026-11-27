import { cn } from "@/lib/utils";
import { ChevronDown, type LucideIcon } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** Whether the card's panel renders its children: always, or under mountWhenOpen once opened. Pure. */
export function panelShown(mountWhenOpen: boolean | undefined, opened: boolean): boolean {
	return !mountWhenOpen || opened;
}

export interface ActionCardProps {
	/** Kept for callers; the card no longer draws an icon tile (X-34). */
	icon?: LucideIcon;
	title: string;
	/** Shown in full, never clamped. */
	description: string;
	/** Shown at the right of the header instead of the open/hide toggle (for one-click actions). */
	action?: React.ReactNode;
	/** Small status line under the title (a pill, a result). */
	status?: React.ReactNode;
	/** The form or result, in a collapsible panel. Omit for a header-only card. */
	children?: React.ReactNode;
	defaultOpen?: boolean;
	/**
	 * Mount the panel's children only once the card is first opened (Q-20, V4-1): a closed card then
	 * asks the server for nothing. Default false: the panel stays in the DOM while closed.
	 */
	mountWhenOpen?: boolean;
	testId?: string;
	className?: string;
}

/**
 * A compact action card (lane U20): a title, a short description and one button. The form or the
 * results sit in a native disclosure panel, so they stay in the DOM (and in server renders) but
 * out of sight until opened.
 */
export function ActionCard({
	title,
	description,
	action,
	status,
	children,
	defaultOpen,
	mountWhenOpen,
	testId,
	className,
}: ActionCardProps) {
	const { t } = useTranslation("compareUx");
	const [opened, setOpened] = useState(Boolean(defaultOpen));
	// Q-177: in the stacked phone layout (flex-col) a basis is a height, and basis-48 left about 150 px
	// of empty space under the text; the basis applies only where the head sits in a row.
	const head = (
		<>
			<span className={cn("flex min-w-0 flex-1 flex-col gap-0.5", children ? "basis-48" : "sm:basis-48")}>
				<span className="text-foreground text-sm font-semibold">{title}</span>
				<span className="text-muted-foreground text-xs">{description}</span>
				{status}
			</span>
		</>
	);
	const shell = cn("bg-card rounded-xl border transition-shadow hover:shadow-md", className);

	if (!children) {
		return (
			<div className={cn(shell, "flex flex-col items-start gap-3 p-4 sm:flex-row sm:items-center")} data-testid={testId}>
				{head}
				{action && <span className="shrink-0">{action}</span>}
			</div>
		);
	}

	return (
		<details
			className={cn(shell, "group")}
			open={defaultOpen}
			onToggle={(e) => {
				if ((e.currentTarget as HTMLDetailsElement).open) setOpened(true);
			}}
			data-testid={testId}
		>
			{/* M4-36: the action and the open button wrap under the text on a phone, never squeeze it. */}
			<summary className="flex cursor-pointer list-none flex-wrap items-center gap-3 p-4 [&::-webkit-details-marker]:hidden">
				{head}
				{action ? (
					<span
						className="shrink-0"
						onClick={(e) => {
							// A click on the action must not toggle the panel.
							e.preventDefault();
							e.stopPropagation();
						}}
					>
						{action}
					</span>
				) : null}
				<span className="border-input text-foreground hover:bg-accent inline-flex shrink-0 items-center gap-1 rounded-md border px-2.5 py-1 text-xs font-medium">
					<span className="group-open:hidden">{t("card.open")}</span>
					<span className="hidden group-open:inline">{t("card.close")}</span>
					<ChevronDown className="size-3.5 transition-transform group-open:rotate-180" aria-hidden="true" />
				</span>
			</summary>
			<div className="border-t px-4 pt-4 pb-5">{panelShown(mountWhenOpen, opened) ? children : null}</div>
		</details>
	);
}