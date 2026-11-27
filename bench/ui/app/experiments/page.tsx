import { PageWidth } from "@/components/shell/pageWidth";
import ExperimentListView from "./views/experimentListView";

// Composition only. The screen lives in views/.
export default function ExperimentsPage() {
	return (
		<PageWidth kind="wide">
			<ExperimentListView />
		</PageWidth>
	);
}