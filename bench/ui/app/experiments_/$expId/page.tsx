import { PageWidth } from "@/components/shell/pageWidth";
import { getRouteApi } from "@tanstack/react-router";
import ExperimentView from "./views/experimentView";

const route = getRouteApi("/experiments_/$expId");

// Composition only. The screen lives in views/. Keyed by the id so a route change never shows the
// previous experiment's status, or arms its Start, while the new one loads (QC2-28-04).
export default function ExperimentPage() {
	const { expId } = route.useParams();
	return (
		<PageWidth kind="dash">
			<ExperimentView key={expId} expId={expId} />
		</PageWidth>
	);
}