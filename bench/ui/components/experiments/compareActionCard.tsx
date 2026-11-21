import { cn } from "@/lib/utils";
import { ChevronDown, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";

export interface ActionCardProps {
	icon: LucideIcon;
	title: string;
	/** One line; clamped. */
	description: string;
	/** Shown at the right of the header instead of the open/hide toggle (for one-click actions). */
	action?: React.ReactNode;
	/** Small status line under the title (a pill, a result). */
	status?: React.ReactNode;
	/** The form or result, in a collapsible panel. Omit for a header-only card. */
	children?: React.ReactNode;
	defaultOpen?: boolean;
	testId?: string;
	className?: string;
}

/**
 * A compact action card (lane U20): an icon, a title, one line and one button. The form or the
 * results sit in a native disclosure panel, so they stay in the DOM (and in server renders) but
 * out of sight until opened.
 */
export function ActionCard({ icon: Icon, title, description, action, status, children, defaultOpen, testId, className }: ActionCardProps) {
	const { t } = useTranslation("compareUx");
	const head = (
		<>
			<span className="bg-primary/10 text-primary flex size-9 shrink-0 items-center justify-center rounded-lg" aria-hidden="true">
				<Icon className="size-4.5" />
			</span>
			<span className="flex min-w-0 flex-1 flex-col gap-0.5">
				<span className="text-foreground text-sm font-semibold">{title}</span>
				<span className="text-muted-foreground line-clamp-1 text-xs" title={description}>
					{description}
				</span>
				{status}
			</span>
		</>
	);
	const shell = cn("bg-card rounded-xl border transition-shadow hover:shadow-md", className);

	if (!children) {
		return (
			<div className={cn(shell, "flex items-center gap-3 p-4")} data-testid={testId}>
				{head}
				{action && <span className="shrink-0">{action}</span>}
			</div>
		);
	}

	return (
		<details className={cn(shell, "group")} open={defaultOpen} data-testid={testId}>
			<summary className="flex cursor-pointer list-none items-center gap-3 p-4 [&::-webkit-details-marker]:hidden">
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
			<div className="border-t px-4 pt-4 pb-5">{children}</div>
		</details>
	);
}
