import { PageWidth } from "@/components/shell/pageWidth";
import MonitorsView from "./views/monitorsView";

// Composition only. The screen lives in views/.
export default function MonitorsPage() {
	return (
		<PageWidth kind="wide">
			<MonitorsView />
		</PageWidth>
	);
}