import { Outlet, createRootRoute } from "@tanstack/react-router";
import { ClientLayout } from "./clientLayout";
import { RootErrorComponent } from "./__error";
import { NotFoundComponent } from "./__notFound";

/**
 * Root route. Providers and the shell live in ClientLayout, as ui/ keeps them in
 * app/clientLayout.tsx, so screen folders carry none of it and can move into ui/ unchanged.
 * "/" is Home (app/index).
 */
export const Route = createRootRoute({
	component: RootComponent,
	notFoundComponent: NotFoundComponent,
	errorComponent: RootErrorComponent,
});

function RootComponent() {
	return (
		<ClientLayout>
			<Outlet />
		</ClientLayout>
	);
}