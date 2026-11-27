import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { Info } from "lucide-react";
import { useTranslation } from "react-i18next";

/** Statistics with a method note on the verdict screen (DESIGN.md 9.3, 9.5). */
export type MethodTopic = "pValue" | "mde" | "costPerSuccess";

export interface MethodHintProps {
	topic: MethodTopic;
	className?: string;
}

/** An info button whose tooltip gives the definition and the denominator, one sentence each. */
export function MethodHint({ topic, className }: MethodHintProps) {
	const { t } = useTranslation("bench");
	const label = t("verdict.method.label", { name: t(`verdict.method.${topic}.name`) });
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					type="button"
					aria-label={label}
					data-testid={`verdict-method-${topic}`}
					className={cn(
						"text-muted-foreground hover:text-foreground focus-visible:ring-ring/50 inline-flex size-6 cursor-help items-center justify-center rounded-full align-middle focus-visible:ring-[3px]",
						className,
					)}
				>
					<Info className="size-3.5" aria-hidden="true" />
				</button>
			</TooltipTrigger>
			<TooltipContent className="max-w-sm text-left" data-testid={`verdict-method-${topic}-content`}>
				<p>{t(`verdict.method.${topic}.definition`)}</p>
				<p className="text-muted-foreground mt-1">{t(`verdict.method.${topic}.denominator`)}</p>
			</TooltipContent>
		</Tooltip>
	);
}