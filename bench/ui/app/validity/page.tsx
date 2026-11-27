import { PageWidth } from "@/components/shell/pageWidth";
import ValidityView from "./views/validityView";

// Composition only. The screen lives in views/.
export default function ValidityPage() {
	return (
		<PageWidth kind="wide">
			<ValidityView />
		</PageWidth>
	);
}