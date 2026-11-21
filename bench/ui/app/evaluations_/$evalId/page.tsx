import { getRouteApi } from "@tanstack/react-router";
import EvaluationView from "./views/evaluationView";

const route = getRouteApi("/evaluations_/$evalId");

// Composition only. Keyed by the id so a route change never shows the previous evaluation.
export default function EvaluationPage() {
	const { evalId } = route.useParams();
	return <EvaluationView key={evalId} evalId={evalId} />;
}