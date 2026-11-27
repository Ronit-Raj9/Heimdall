import { PageWidth } from "@/components/shell/pageWidth";
import AddBenchmarkView from "./views/addBenchmarkView";

// Composition only. The screen lives in views/.
export default function AddBenchmarkPage() {
	return (
		<PageWidth kind="read">
			<AddBenchmarkView />
		</PageWidth>
	);
}
