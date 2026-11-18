import { getRouteApi } from "@tanstack/react-router";
import ExperimentView from "./views/experimentView";

const route = getRouteApi("/experiments_/$expId");

// Composition only. The screen lives in views/.
export default function ExperimentPage() {
	const { expId } = route.useParams();
	return <ExperimentView expId={expId} />;
}