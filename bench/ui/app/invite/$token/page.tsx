import { getRouteApi } from "@tanstack/react-router";
import InviteView from "./views/inviteView";

const route = getRouteApi("/invite/$token");

// Composition only. The screen lives in views/.
export default function InvitePage() {
	const { token } = route.useParams();
	return <InviteView token={token} />;
}