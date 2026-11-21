import { getRouteApi } from "@tanstack/react-router";
import ResetPasswordView from "./views/resetPasswordView";

const route = getRouteApi("/reset-password");

// Composition only. The screen lives in views/.
export default function ResetPasswordPage() {
	const { token } = route.useSearch();
	return <ResetPasswordView token={token} />;
}
