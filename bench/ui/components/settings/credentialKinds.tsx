import "./uf8aI18n";
import { DetailsDisclosure } from "@/components/ui/detailsDisclosure";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

/** The four kinds of key and token, each on its own Settings tab. */
export const CREDENTIAL_KINDS = ["keys", "secrets", "tokens", "runners"] as const;
export type CredentialKind = (typeof CREDENTIAL_KINDS)[number];

/**
 * "Which one do I need?": a collapsed card at the top of the four credential tabs that tells the
 * kinds apart (what it is, who uses it) and links to each tab. The current tab is marked.
 */
export function CredentialKinds({ current }: { current: CredentialKind }) {
	const { t } = useTranslation("uf8a");
	return (
		<DetailsDisclosure label={t("kinds.title")} testId="credential-kinds">
			<div className="bg-card overflow-hidden rounded-xl border">
				<ul className="divide-y">
					{CREDENTIAL_KINDS.map((kind) => (
						<li
							key={kind}
							className="grid gap-1 px-4 py-3 text-sm sm:grid-cols-[9rem_minmax(0,1fr)_12rem] sm:gap-4"
							data-testid={`credential-kind-${kind}`}
						>
							{kind === current ? (
								<span className="font-medium">
									{t(`kinds.${kind}.name`)}
									<span className="text-muted-foreground block text-xs font-normal">{t("kinds.here")}</span>
								</span>
							) : (
								<Link
									to="/settings"
									search={{ tab: kind } as never}
									className="text-foreground font-medium underline-offset-4 hover:underline"
								>
									{t(`kinds.${kind}.name`)}
								</Link>
							)}
							<span className="text-muted-foreground">{t(`kinds.${kind}.what`)}</span>
							<span className="text-muted-foreground text-xs sm:text-sm">
								<span className="sr-only">{t("kinds.colWho")}: </span>
								{t(`kinds.${kind}.who`)}
							</span>
						</li>
					))}
				</ul>
			</div>
		</DetailsDisclosure>
	);
}