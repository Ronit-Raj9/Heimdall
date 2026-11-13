import { Outlet, createRootRoute, redirect } from "@tanstack/react-router";
import { ClientLayout } from "./clientLayout";
import { NotFoundComponent } from "./__notFound";

/**
 * Root route. Providers and the shell live in ClientLayout, as ui/ keeps them in
 * app/clientLayout.tsx, so screen folders carry none of it and can move into ui/ unchanged.
 */
export const Route = createRootRoute({
	beforeLoad: ({ location }) => {
		if (location.pathname === "/" || location.pathname === "") {
			throw redirect({ to: "/verdict" });
		}
	},
	component: RootComponent,
	notFoundComponent: NotFoundComponent,
});

function RootComponent() {
	return (
		<ClientLayout>
			<Outlet />
		</ClientLayout>
	);
}