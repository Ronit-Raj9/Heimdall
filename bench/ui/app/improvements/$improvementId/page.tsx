import { getRouteApi } from "@tanstack/react-router";
import ImprovementView from "./views/improvementView";

const route = getRouteApi("/improvements/$improvementId");

// Composition only. The screen lives in views/.
export default function ImprovementPage() {
	const { improvementId } = route.useParams();
	return <ImprovementView id={improvementId} />;
}
