import { getRouteApi } from "@tanstack/react-router";
import RunView from "./views/runView";

const route = getRouteApi("/runs_/$runId");

// Composition only. The screen lives in views/.
export default function RunPage() {
	const { runId } = route.useParams();
	return <RunView runId={runId} />;
}