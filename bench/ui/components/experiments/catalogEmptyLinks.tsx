import { Button } from "@/components/ui/button";
import { Link } from "@tanstack/react-router";
import { Bot, Library, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * The next steps when the catalog has no suite or no agent, so no experiment can be planned: add a
 * benchmark from the catalog, write a new suite, or create an agent. Only the missing side's links
 * show.
 */
export function CatalogEmptyLinks({ message, noSuites, noAgents }: { message: string; noSuites: boolean; noAgents: boolean }) {
	const { t: tc } = useTranslation("catalog");
	const { t: ts } = useTranslation("suiteAuthoring");
	const { t: ta } = useTranslation("agentBuilder");
	const both = !noSuites && !noAgents; // defaults could not be built: offer every way out
	return (
		<div
			className="bg-muted/30 flex flex-col items-center gap-3 rounded-lg border border-dashed px-4 py-8 text-center"
			data-testid="new-experiment-catalog-empty"
		>
			<p className="text-muted-foreground text-sm text-pretty">{message}</p>
			<div className="flex flex-wrap justify-center gap-2">
				{(noSuites || both) && (
					<>
						<Button asChild size="sm">
							<Link to="/catalog" data-testid="catalog-empty-catalog">
								<Library className="size-4" aria-hidden="true" />
								{tc("title")}
							</Link>
						</Button>
						<Button asChild size="sm" variant="outline">
							<Link to="/suites/new" data-testid="catalog-empty-new-suite">
								<Plus className="size-4" aria-hidden="true" />
								{ts("new.title")}
							</Link>
						</Button>
					</>
				)}
				{(noAgents || both) && (
					<Button asChild size="sm" variant="outline">
						<Link to="/agents/new" data-testid="catalog-empty-new-agent">
							<Bot className="size-4" aria-hidden="true" />
							{ta("new.title")}
						</Link>
					</Button>
				)}
			</div>
		</div>
	);
}
