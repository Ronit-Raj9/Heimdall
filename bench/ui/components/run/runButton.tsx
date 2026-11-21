import type { ModelRef } from "@/components/models/modelPicker";
import { Button } from "@/components/ui/button";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { QuickRunDialog } from "./quickRunDialog";

export interface RunButtonProps {
	suite?: string;
	agent?: string;
	model?: ModelRef;
	tasks?: number;
	label?: string;
}

/** Opens the QuickRun dialog with whatever is missing; the dialog makes one POST /quickrun (lane L28). */
export function RunButton({ suite, agent, model, tasks, label }: RunButtonProps) {
	const { t } = useTranslation("quickrun");
	const [open, setOpen] = useState(false);
	return (
		<>
			<Button onClick={() => setOpen(true)} data-testid="run-button">
				{label ?? t("button")}
			</Button>
			{open && <QuickRunDialog suite={suite} agent={agent} model={model} tasks={tasks} onClose={() => setOpen(false)} />}
		</>
	);
}

export default RunButton;
