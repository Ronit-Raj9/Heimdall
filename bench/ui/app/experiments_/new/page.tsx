import { PageWidth } from "@/components/shell/pageWidth";
import NewExperimentView from "./views/newExperimentView";

// Composition only. The screen lives in views/.
export default function NewExperimentPage() {
	return (
		<PageWidth kind="read">
			<NewExperimentView />
		</PageWidth>
	);
}