import { QuickRunDialog } from "@/components/run/quickRunDialog";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { Play } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { suiteDisplayName } from "./suiteDisplay";

/** Opens QuickRun with this suite already picked (lane U04). */
export function SuiteRunButton({
	suiteId,
	size = "sm",
	variant = "default",
	testId,
	className,
	quiet = false,
}: {
	suiteId: string;
	size?: "sm" | "default" | "lg";
	variant?: "default" | "outline" | "secondary" | "ghost";
	testId?: string;
	className?: string;
	/** A plain text button with no icon, for list rows. */
	quiet?: boolean;
}) {
	const { t } = useTranslation("suitesUx");
	const [open, setOpen] = useState(false);
	return (
		<>
			<Button
				size={size}
				variant={variant}
				onClick={() => setOpen(true)}
				aria-label={t("runSuite", { suite: suiteDisplayName(suiteId) })}
				data-testid={testId ?? "suite-run-button"}
				className={cn(quiet && "text-muted-foreground hover:text-foreground h-7 px-2", className)}
			>
				{!quiet && <Play className="size-4" aria-hidden="true" />}
				{t("run")}
			</Button>
			{open && <QuickRunDialog suite={suiteId} onClose={() => setOpen(false)} />}
		</>
	);
}
