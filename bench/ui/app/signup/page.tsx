import { getRouteApi } from "@tanstack/react-router";
import SignupView from "./views/signupView";

const route = getRouteApi("/signup");

// Composition only. The screen lives in views/.
export default function SignupPage() {
	const { invitation } = route.useSearch();
	return <SignupView invitation={invitation} />;
}