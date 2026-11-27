import { PageWidth } from "@/components/shell/pageWidth";
import LibraryView from "./views/libraryView";

// Composition only. The screen lives in views/.
export default function LibraryPage() {
	return (
		<PageWidth kind="wide">
			<LibraryView />
		</PageWidth>
	);
}
