import glossary from "@/locales/en/glossary.json";
import i18n from "@/lib/i18n";
import "@/lib/types/glossaryI18n";
import { cn } from "@/lib/utils";
import { useId, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { Tooltip, TooltipContent, TooltipTrigger } from "./tooltip";

/** A glossary entry key (locales/en/glossary.json terms). */
export type GlossaryKey = keyof typeof glossary.terms;

/** Every glossary key, in catalog order. */
export const GLOSSARY_KEYS = Object.keys(glossary.terms) as GlossaryKey[];

/**
 * The plain one-line explanation of a term, for a `title=` attribute or any place a component
 * cannot be used. Unknown keys return an empty string.
 */
export function termTip(k: GlossaryKey): string {
	if (!(k in glossary.terms)) return "";
	return i18n.t(`terms.${k}.tip`, { ns: "glossary" });
}

/** The plain label of a term (what to show instead of the internal word). */
export function termLabel(k: GlossaryKey): string {
	if (!(k in glossary.terms)) return k;
	return i18n.t(`terms.${k}.label`, { ns: "glossary" });
}

export interface TermProps {
	/** Glossary key: the internal word this label stands for. */
	k: GlossaryKey;
	/** What to show. Defaults to the glossary's plain label. */
	children?: ReactNode;
	/** Adds a small "(technical: ...)" hint inside the tooltip for experts. */
	showTechnical?: boolean;
	className?: string;
	testId?: string;
}

/**
 * A word with a plain-language explanation: dotted underline, tooltip on hover and keyboard
 * focus, and the explanation linked by aria-describedby so screen readers read it too.
 *
 *   <Term k="admitted">Ready</Term>
 */
export function Term({ k, children, showTechnical = false, className, testId }: TermProps) {
	const { t } = useTranslation("glossary");
	const id = useId();
	const tipId = `${id}-tip`;
	const tip = termTip(k);
	const label = children ?? termLabel(k);
	if (!tip) return <>{label}</>;
	const technical = showTechnical ? t("technical", { term: glossary.terms[k].term }) : null;
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span
					tabIndex={0}
					aria-describedby={tipId}
					data-term={k}
					data-testid={testId}
					className={cn(
						"decoration-muted-foreground/70 cursor-help underline decoration-dotted underline-offset-[3px]",
						"focus-visible:ring-(--focus) rounded-sm focus-visible:ring-2 focus-visible:outline-none",
						className,
					)}
				>
					{label}
				</span>
			</TooltipTrigger>
			<span id={tipId} className="sr-only">
				{tip}
			</span>
			<TooltipContent>
				{tip}
				{technical && <span className="text-muted-foreground mt-0.5 block text-[11px]">({technical})</span>}
			</TooltipContent>
		</Tooltip>
	);
}
