import { getRouteApi } from "@tanstack/react-router";
import VerifyEmailView from "./views/verifyEmailView";

const route = getRouteApi("/verify-email");

// Composition only. The screen lives in views/.
export default function VerifyEmailPage() {
	const { token } = route.useSearch();
	return <VerifyEmailView token={token} />;
}
