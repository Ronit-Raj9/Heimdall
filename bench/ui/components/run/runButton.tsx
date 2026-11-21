import { Button } from "@/components/ui/button";
import type { ModelRef } from "@/components/models/modelPicker";

export interface RunButtonProps {
	suite?: string;
	agent?: string;
	model?: ModelRef;
	tasks?: number;
	label?: string;
}

/** Opens the QuickRun dialog with whatever is missing (web parity Phase 0 stub; lane L28 builds it). */
export function RunButton({ label }: RunButtonProps) {
	return (
		<Button disabled data-testid="run-button">
			{label ?? "Run"}
		</Button>
	);
}

export default RunButton;
