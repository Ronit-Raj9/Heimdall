import { PageWidth } from "@/components/shell/pageWidth";
import FromApiView from "./views/fromApiView";

// Composition only. The screen lives in views/.
export default function FromApiPage() {
	return (
		<PageWidth kind="read">
			<FromApiView />
		</PageWidth>
	);
}