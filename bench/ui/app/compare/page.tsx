import { PageWidth } from "@/components/shell/pageWidth";
import CompareView from "./views/compareView";

// Composition only. The screen lives in views/.
export default function ComparePage() {
	return (
		<PageWidth kind="dash">
			<CompareView />
		</PageWidth>
	);
}