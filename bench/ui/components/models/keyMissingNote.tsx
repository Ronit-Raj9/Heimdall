import "@/components/settings/uf8aI18n";
import { Button } from "@/components/ui/button";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

/** The Settings search that opens the Add API key panel for a provider and comes back to returnTo. */
export function keyMissingSearch(provider: string, returnTo?: string): { tab: "keys"; add?: string; return?: string } {
	return {
		tab: "keys",
		...(provider ? { add: provider } : {}),
		...(returnTo && returnTo.startsWith("/") && !returnTo.startsWith("//") ? { return: returnTo } : {}),
	};
}

export interface KeyMissingNoteProps {
	/** Provider id, such as "anthropic"; opens the panel with it set. */
	provider: string;
	/** Display name of the provider, for the text. Defaults to the id. */
	providerName?: string;
	/** In-app path to come back to after the key is saved (the model keys tab shows a banner). */
	returnTo?: string;
	className?: string;
}

/**
 * "No working <provider> key" with a button that opens Settings, Model keys, on the Add API key
 * panel for that provider. After Save the tab offers a way back to returnTo.
 */
export function KeyMissingNote({ provider, providerName, returnTo, className }: KeyMissingNoteProps) {
	const { t } = useTranslation("uf8a");
	const name = providerName || provider;
	return (
		<div className={className ?? "flex flex-wrap items-center gap-2 text-sm"} data-testid="key-missing-note">
			<span>{t("models.keyMissing", { provider: name })}</span>
			<Button asChild size="sm" variant="outline">
				<Link to="/settings" search={keyMissingSearch(provider, returnTo) as never}>
					{t("models.keyMissingAction", { provider: name })}
				</Link>
			</Button>
		</div>
	);
}