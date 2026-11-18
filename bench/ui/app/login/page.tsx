import { getRouteApi } from "@tanstack/react-router";
import LoginView from "./views/loginView";

const route = getRouteApi("/login");

// Composition only. The screen lives in views/.
export default function LoginPage() {
	const { return_to, error } = route.useSearch();
	return <LoginView returnTo={return_to} oauthError={error} />;
}