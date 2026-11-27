import { PageWidth } from "@/components/shell/pageWidth";
import NewSuiteView from "./views/newSuiteView";

// Composition only. The screen lives in views/.
export default function NewSuitePage() {
	return (
		<PageWidth kind="read">
			<NewSuiteView />
		</PageWidth>
	);
}
