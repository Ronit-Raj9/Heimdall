import { QuickRunDialog } from "@/components/run/quickRunDialog";
import { Button } from "@/components/ui/button";
import { Play } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** Opens QuickRun with this suite already picked (lane U04). */
export function SuiteRunButton({
	suiteId,
	size = "sm",
	variant = "default",
	testId,
	className,
}: {
	suiteId: string;
	size?: "sm" | "default" | "lg";
	variant?: "default" | "outline" | "secondary";
	testId?: string;
	className?: string;
}) {
	const { t } = useTranslation("suitesUx");
	const [open, setOpen] = useState(false);
	return (
		<>
			<Button
				size={size}
				variant={variant}
				onClick={() => setOpen(true)}
				aria-label={t("runSuite", { suite: suiteId })}
				data-testid={testId ?? "suite-run-button"}
				className={className}
			>
				<Play className="size-4" aria-hidden="true" />
				{t("run")}
			</Button>
			{open && <QuickRunDialog suite={suiteId} onClose={() => setOpen(false)} />}
		</>
	);
}
