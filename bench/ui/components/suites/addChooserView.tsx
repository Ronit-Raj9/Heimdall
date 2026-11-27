import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { ADD_ENTRIES, type AddEntryId } from "./addChooser";
import "./uf6bI18n";

/**
 * The chooser "How do you want to add tasks?": the six ways in, each with one line on what it
 * needs. `current` marks the row of the page it is shown on.
 */
export function AddChooser({ current, className }: { current?: AddEntryId; className?: string }) {
	const { t } = useTranslation("uf6b");
	return (
		<nav aria-label={t("chooser.title")} className={cn("flex min-w-0 flex-col gap-2", className)} data-testid="add-chooser">
			<h2 className="text-sm font-medium">{t("chooser.title")}</h2>
			<ul className="grid min-w-0 grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
				{ADD_ENTRIES.map((e) => (
					<li key={e.id} className="flex min-w-0 flex-col gap-1">
						<Link
							to={e.to}
							search={e.search as never}
							aria-current={current === e.id ? "page" : undefined}
							className={cn(
								"hover:bg-muted/60 flex flex-1 min-w-0 flex-col gap-0.5 rounded-md border px-3 py-2 text-sm",
								current === e.id && "border-primary bg-primary-soft",
							)}
							data-testid={`add-chooser-${e.id}`}
						>
							<span className="font-medium">{t(`chooser.${e.id}.label`)}</span>
							<span className="text-muted-foreground text-xs text-pretty">{t(`chooser.${e.id}.needs`)}</span>
						</Link>
						{e.id === "failures" && (
							// M6-32: traces kept in another platform are imported through the catalog importers.
							<Link
								to="/suites"
								search={{ tab: "catalog" } as never}
								className="text-muted-foreground hover:text-foreground px-3 text-xs underline underline-offset-2"
								data-testid="add-chooser-platform-import"
							>
								{t("chooser.platformImport")}
							</Link>
						)}
					</li>
				))}
			</ul>
		</nav>
	);
}
