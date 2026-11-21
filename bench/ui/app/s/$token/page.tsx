import { getRouteApi } from "@tanstack/react-router";
import SharedView from "./views/sharedView";

const route = getRouteApi("/s/$token");

// Composition only. The screen lives in views/.
export default function SharedPage() {
	const { token } = route.useParams();
	return <SharedView token={token} />;
}