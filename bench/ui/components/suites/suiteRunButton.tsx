import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { Play } from "lucide-react";
import { useTranslation } from "react-i18next";
import { suiteDisplayName } from "./suiteDisplay";

/**
 * Runs an agent on this benchmark through /evaluate (the one start path), with the benchmark
 * already picked. The quick single run is an advanced link on the /evaluate page.
 */
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
	return (
		<Button
			asChild
			size={size}
			variant={variant}
			className={cn(quiet && "text-muted-foreground hover:text-foreground h-7 px-2", className)}
		>
			<Link
				to={"/evaluate" as never}
				search={{ suite: suiteId } as never}
				aria-label={t("runSuite", { suite: suiteDisplayName(suiteId) })}
				data-testid={testId ?? "suite-run-button"}
			>
				{!quiet && <Play className="size-4" aria-hidden="true" />}
				{t("run")}
			</Link>
		</Button>
	);
}
